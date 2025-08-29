import React, { useState, useEffect, useRef, useCallback } from 'react';

const RTSPStreamManager = ({ 
  serverUrl = 'http://localhost:8091',
  defaultRtspUrl = 'rtsp://172.16.212.201:8554/mystream'
}) => {
  const [streamId, setStreamId] = useState(null);
  const [rtspUrl, setRtspUrl] = useState(defaultRtspUrl);
  const [isConnected, setIsConnected] = useState(false);
  const [streamStatus, setStreamStatus] = useState(null);
  const [error, setError] = useState(null);
  const [isLoading, setIsLoading] = useState(false);
  
  // WebSocket and Canvas refs
  const wsRef = useRef(null);
  const canvasRef = useRef(null);
  const statusIntervalRef = useRef(null);
  
  // Stream statistics
  const [stats, setStats] = useState({
    frameCount: 0,
    clientCount: 0,
    errorCount: 0,
    lastFrameTime: null,
    secondsSinceLastFrame: 0
  });

  // Fetch stream status periodically
  const fetchStreamStatus = useCallback(async (currentStreamId) => {
    if (!currentStreamId) return;
    
    try {
      const response = await fetch(`${serverUrl}/api/streams/${currentStreamId}/status`);
      const status = await response.json();
      
      if (response.ok) {
        setStreamStatus(status);
        setStats({
          frameCount: status.frame_count || 0,
          clientCount: status.client_count || 0,
          errorCount: status.error_count || 0,
          lastFrameTime: status.last_frame_time,
          secondsSinceLastFrame: status.seconds_since_last_frame || 0
        });
        
        // Clear error if stream is running well
        if (status.status === 'running' && status.is_running) {
          setError(null);
        } else if (status.status === 'error' || status.status === 'failed') {
          setError(status.last_error || `Stream status: ${status.status}`);
        }
      }
    } catch (err) {
      console.error('Failed to fetch stream status:', err);
    }
  }, [serverUrl]);

  // Start stream
  const startStream = async () => {
    if (!rtspUrl.trim()) {
      setError('Please enter a valid RTSP URL');
      return;
    }
    
    setIsLoading(true);
    setError(null);
    
    try {
      const response = await fetch(`${serverUrl}/api/streams/start-with-url`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          rtsp_url: rtspUrl,
          width: 640,
          height: 480
        })
      });
      
      const result = await response.json();
      
      if (response.ok) {
        const newStreamId = result.stream_id;
        setStreamId(newStreamId);
        
        if (result.message?.includes('already running')) {
          console.log('Stream already running, connecting to existing stream');
        }
        
        // Wait a moment for stream to initialize if it's new
        if (!result.message?.includes('already running')) {
          await new Promise(resolve => setTimeout(resolve, 3000));
        }
        
        connectWebSocket(newStreamId);
        
        // Start status monitoring
        statusIntervalRef.current = setInterval(() => {
          fetchStreamStatus(newStreamId);
        }, 2000);
        
      } else {
        throw new Error(result.error || 'Failed to start stream');
      }
    } catch (err) {
      setError(`Failed to start stream: ${err.message}`);
      console.error('Stream start error:', err);
    } finally {
      setIsLoading(false);
    }
  };

  // Connect WebSocket
  const connectWebSocket = (currentStreamId) => {
    if (wsRef.current) {
      wsRef.current.close();
    }
    
    try {
      const ws = new WebSocket(`ws://localhost:8091/ws/${currentStreamId}`);
      wsRef.current = ws;
      
      ws.onopen = () => {
        console.log('WebSocket connected');
        setIsConnected(true);
        setError(null);
      };
      
      ws.onmessage = (event) => {
        if (event.data instanceof Blob) {
          const reader = new FileReader();
          reader.onload = () => {
            const arrayBuffer = reader.result;
            displayFrame(new Uint8Array(arrayBuffer));
          };
          reader.readAsArrayBuffer(event.data);
        }
      };
      
      ws.onerror = (error) => {
        console.error('WebSocket error:', error);
        setError('WebSocket connection error');
        setIsConnected(false);
      };
      
      ws.onclose = (event) => {
        console.log('WebSocket closed:', event.code, event.reason);
        setIsConnected(false);
        
        if (event.code !== 1000) { // Not a normal closure
          setError(`WebSocket closed unexpectedly: ${event.reason || 'Connection lost'}`);
        }
      };
      
    } catch (err) {
      setError(`Failed to connect WebSocket: ${err.message}`);
      console.error('WebSocket connection error:', err);
    }
  };

  // Display frame on canvas
  const displayFrame = (frameData) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    
    const ctx = canvas.getContext('2d');
    const width = canvas.width;
    const height = canvas.height;
    
    if (frameData.length !== width * height * 3) {
      console.warn('Invalid frame size received');
      return;
    }
    
    const imageData = ctx.createImageData(width, height);
    const data = imageData.data;
    
    // Convert BGR to RGBA
    for (let i = 0; i < width * height; i++) {
      const bgrIndex = i * 3;
      const rgbaIndex = i * 4;
      
      data[rgbaIndex] = frameData[bgrIndex + 2];     // R
      data[rgbaIndex + 1] = frameData[bgrIndex + 1]; // G
      data[rgbaIndex + 2] = frameData[bgrIndex];     // B
      data[rgbaIndex + 3] = 255;                     // A
    }
    
    ctx.putImageData(imageData, 0, 0);
  };

  // Stop stream
  const stopStream = async () => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    
    if (statusIntervalRef.current) {
      clearInterval(statusIntervalRef.current);
      statusIntervalRef.current = null;
    }
    
    if (streamId) {
      try {
        await fetch(`${serverUrl}/api/streams/${streamId}`, {
          method: 'DELETE'
        });
      } catch (err) {
        console.error('Failed to stop stream:', err);
      }
    }
    
    setStreamId(null);
    setIsConnected(false);
    setStreamStatus(null);
    setError(null);
    setStats({
      frameCount: 0,
      clientCount: 0,
      errorCount: 0,
      lastFrameTime: null,
      secondsSinceLastFrame: 0
    });
    
    // Clear canvas
    const canvas = canvasRef.current;
    if (canvas) {
      const ctx = canvas.getContext('2d');
      ctx.clearRect(0, 0, canvas.width, canvas.height);
    }
  };

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      stopStream();
    };
  }, []);

  // Status indicator
  const getStatusIndicator = () => {
    if (isLoading) return { color: '#ffa500', text: 'Starting...' };
    if (error) return { color: '#ff4444', text: 'Error' };
    if (!streamId) return { color: '#888', text: 'Disconnected' };
    if (isConnected && streamStatus?.is_running) return { color: '#44ff44', text: 'Connected' };
    if (streamId && !isConnected) return { color: '#ffaa00', text: 'Connecting...' };
    return { color: '#888', text: 'Unknown' };
  };

  const statusIndicator = getStatusIndicator();

  return (
    <div style={{ padding: '20px', fontFamily: 'Arial, sans-serif' }}>
      <h2>RTSP Stream Manager</h2>
      
      {/* Connection Controls */}
      <div style={{ marginBottom: '20px' }}>
        <div style={{ marginBottom: '10px' }}>
          <input
            type="text"
            value={rtspUrl}
            onChange={(e) => setRtspUrl(e.target.value)}
            placeholder="Enter RTSP URL"
            style={{
              width: '400px',
              padding: '8px',
              marginRight: '10px',
              border: '1px solid #ccc',
              borderRadius: '4px'
            }}
            disabled={isLoading || isConnected}
          />
          
          {!isConnected ? (
            <button
              onClick={startStream}
              disabled={isLoading}
              style={{
                padding: '8px 16px',
                backgroundColor: isLoading ? '#ccc' : '#007acc',
                color: 'white',
                border: 'none',
                borderRadius: '4px',
                cursor: isLoading ? 'not-allowed' : 'pointer'
              }}
            >
              {isLoading ? 'Starting...' : 'Start Stream'}
            </button>
          ) : (
            <button
              onClick={stopStream}
              style={{
                padding: '8px 16px',
                backgroundColor: '#cc0000',
                color: 'white',
                border: 'none',
                borderRadius: '4px',
                cursor: 'pointer'
              }}
            >
              Stop Stream
            </button>
          )}
        </div>
      </div>

      {/* Status Display */}
      <div style={{ 
        marginBottom: '20px',
        padding: '15px',
        backgroundColor: '#f5f5f5',
        borderRadius: '8px',
        border: error ? '2px solid #ff4444' : '1px solid #ddd'
      }}>
        <div style={{ 
          display: 'flex', 
          alignItems: 'center', 
          marginBottom: '10px' 
        }}>
          <div
            style={{
              width: '12px',
              height: '12px',
              borderRadius: '50%',
              backgroundColor: statusIndicator.color,
              marginRight: '8px'
            }}
          />
          <strong>Status: {statusIndicator.text}</strong>
        </div>
        
        {streamId && (
          <div style={{ fontSize: '14px', color: '#666', marginBottom: '5px' }}>
            Stream ID: {streamId}
          </div>
        )}
        
        {streamStatus && (
          <div style={{ fontSize: '14px', color: '#666' }}>
            Stream Status: {streamStatus.status} | 
            Frames: {stats.frameCount} | 
            Clients: {stats.clientCount} | 
            Errors: {stats.errorCount}
            {stats.secondsSinceLastFrame > 0 && (
              <span> | Last frame: {Math.round(stats.secondsSinceLastFrame)}s ago</span>
            )}
          </div>
        )}
        
        {error && (
          <div style={{ 
            color: '#cc0000', 
            fontSize: '14px', 
            marginTop: '10px',
            padding: '8px',
            backgroundColor: '#ffeeee',
            borderRadius: '4px',
            border: '1px solid #ffcccc'
          }}>
            ⚠️ {error}
          </div>
        )}
      </div>

      {/* Video Canvas */}
      <div style={{ textAlign: 'center' }}>
        <canvas
          ref={canvasRef}
          width={640}
          height={480}
          style={{
            border: isConnected ? '2px solid #44ff44' : '2px solid #ccc',
            borderRadius: '8px',
            backgroundColor: '#000'
          }}
        />
      </div>

      {/* Error Recovery Suggestions */}
      {error && (
        <div style={{ 
          marginTop: '20px',
          padding: '15px',
          backgroundColor: '#fff3cd',
          border: '1px solid #ffeaa7',
          borderRadius: '8px'
        }}>
          <h4>Troubleshooting:</h4>
          <ul>
            <li>Check if the RTSP URL is accessible</li>
            <li>Ensure the Go server is running on port 8091</li>
            <li>Verify network connectivity to the camera</li>
            <li>Check if FFmpeg is installed and accessible</li>
            <li>Try stopping and starting the stream again</li>
          </ul>
        </div>
      )}
    </div>
  );
};

export default RTSPStreamManager;
