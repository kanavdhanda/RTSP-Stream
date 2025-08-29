/**
 * RTSP Stream Client with comprehensive error handling
 * Can be used in Wails, Electron, or any web environment
 */
class RTSPStreamClient {
  constructor(options = {}) {
    this.serverUrl = options.serverUrl || 'http://localhost:8091';
    this.canvas = options.canvas;
    this.onStatusChange = options.onStatusChange || (() => {});
    this.onError = options.onError || console.error;
    this.onConnect = options.onConnect || (() => {});
    this.onDisconnect = options.onDisconnect || (() => {});
    
    // Internal state
    this.streamId = null;
    this.websocket = null;
    this.isConnected = false;
    this.statusInterval = null;
    this.currentStatus = 'disconnected';
    this.stats = {
      frameCount: 0,
      clientCount: 0,
      errorCount: 0,
      lastFrameTime: null,
      secondsSinceLastFrame: 0
    };
  }

  /**
   * Start streaming from RTSP URL
   */
  async startStream(rtspUrl, options = {}) {
    const { width = 640, height = 480 } = options;
    
    if (!rtspUrl || !rtspUrl.startsWith('rtsp://')) {
      throw new Error('Invalid RTSP URL provided');
    }
    
    try {
      this.updateStatus('starting');
      
      // Start stream on server
      const response = await fetch(`${this.serverUrl}/api/streams/start-with-url`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          rtsp_url: rtspUrl,
          width,
          height
        })
      });
      
      if (!response.ok) {
        const error = await response.json();
        throw new Error(error.error || `HTTP ${response.status}`);
      }
      
      const result = await response.json();
      this.streamId = result.stream_id;
      
      // Check if stream was already running
      const isExisting = result.message?.includes('already running');
      console.log(isExisting ? 'Connected to existing stream' : 'Started new stream');
      
      // Wait for new streams to initialize
      if (!isExisting) {
        await this.waitForStreamReady(3000);
      }
      
      // Connect WebSocket
      await this.connectWebSocket();
      
      // Start status monitoring
      this.startStatusMonitoring();
      
      this.updateStatus('connected');
      this.onConnect(this.streamId);
      
      return this.streamId;
      
    } catch (error) {
      this.updateStatus('error');
      this.onError(error);
      throw error;
    }
  }

  /**
   * Wait for stream to be ready
   */
  async waitForStreamReady(timeout = 5000) {
    const startTime = Date.now();
    
    while (Date.now() - startTime < timeout) {
      try {
        const status = await this.getStreamStatus();
        if (status.is_running && status.status === 'running') {
          return true;
        }
        if (status.status === 'failed') {
          throw new Error(status.last_error || 'Stream failed to start');
        }
      } catch (err) {
        // Continue waiting
      }
      await new Promise(resolve => setTimeout(resolve, 500));
    }
    
    throw new Error('Stream did not become ready within timeout');
  }

  /**
   * Connect WebSocket for real-time frames
   */
  async connectWebSocket() {
    return new Promise((resolve, reject) => {
      if (this.websocket) {
        this.websocket.close();
      }
      
      const wsUrl = `ws://localhost:8091/ws/${this.streamId}`;
      console.log('Connecting WebSocket:', wsUrl);
      
      this.websocket = new WebSocket(wsUrl);
      
      const timeout = setTimeout(() => {
        reject(new Error('WebSocket connection timeout'));
      }, 10000);
      
      this.websocket.onopen = () => {
        clearTimeout(timeout);
        this.isConnected = true;
        console.log('WebSocket connected');
        resolve();
      };
      
      this.websocket.onmessage = (event) => {
        if (event.data instanceof Blob) {
          this.handleFrameBlob(event.data);
        }
      };
      
      this.websocket.onerror = (error) => {
        clearTimeout(timeout);
        console.error('WebSocket error:', error);
        this.updateStatus('error');
        this.onError(new Error('WebSocket connection failed'));
        reject(error);
      };
      
      this.websocket.onclose = (event) => {
        this.isConnected = false;
        console.log('WebSocket closed:', event.code, event.reason);
        
        if (event.code !== 1000) {
          this.updateStatus('error');
          this.onError(new Error(`WebSocket closed: ${event.reason || 'Connection lost'}`));
        } else {
          this.updateStatus('disconnected');
        }
        
        this.onDisconnect();
      };
    });
  }

  /**
   * Handle incoming frame blob
   */
  handleFrameBlob(blob) {
    const reader = new FileReader();
    reader.onload = () => {
      const arrayBuffer = reader.result;
      this.displayFrame(new Uint8Array(arrayBuffer));
    };
    reader.readAsArrayBuffer(blob);
  }

  /**
   * Display frame on canvas
   */
  displayFrame(frameData) {
    if (!this.canvas) return;
    
    const ctx = this.canvas.getContext('2d');
    const width = this.canvas.width;
    const height = this.canvas.height;
    
    if (frameData.length !== width * height * 3) {
      console.warn('Invalid frame size:', frameData.length, 'expected:', width * height * 3);
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
  }

  /**
   * Start monitoring stream status
   */
  startStatusMonitoring() {
    if (this.statusInterval) {
      clearInterval(this.statusInterval);
    }
    
    this.statusInterval = setInterval(async () => {
      try {
        const status = await this.getStreamStatus();
        this.updateStats(status);
        
        // Check for errors or disconnections
        if (status.status === 'error' || status.status === 'failed') {
          this.updateStatus('error');
          this.onError(new Error(status.last_error || `Stream ${status.status}`));
        } else if (status.is_running && this.isConnected) {
          this.updateStatus('connected');
        }
        
      } catch (err) {
        console.warn('Failed to fetch stream status:', err);
      }
    }, 2000);
  }

  /**
   * Get current stream status from server
   */
  async getStreamStatus() {
    if (!this.streamId) {
      throw new Error('No active stream');
    }
    
    const response = await fetch(`${this.serverUrl}/api/streams/${this.streamId}/status`);
    if (!response.ok) {
      throw new Error(`Failed to get status: HTTP ${response.status}`);
    }
    
    return response.json();
  }

  /**
   * Update internal stats
   */
  updateStats(status) {
    this.stats = {
      frameCount: status.frame_count || 0,
      clientCount: status.client_count || 0,
      errorCount: status.error_count || 0,
      lastFrameTime: status.last_frame_time,
      secondsSinceLastFrame: status.seconds_since_last_frame || 0
    };
  }

  /**
   * Update status and notify listeners
   */
  updateStatus(newStatus) {
    if (this.currentStatus !== newStatus) {
      this.currentStatus = newStatus;
      this.onStatusChange(newStatus, this.stats);
    }
  }

  /**
   * Stop streaming
   */
  async stopStream() {
    // Clear status monitoring
    if (this.statusInterval) {
      clearInterval(this.statusInterval);
      this.statusInterval = null;
    }
    
    // Close WebSocket
    if (this.websocket) {
      this.websocket.close();
      this.websocket = null;
    }
    
    // Stop stream on server
    if (this.streamId) {
      try {
        await fetch(`${this.serverUrl}/api/streams/${this.streamId}`, {
          method: 'DELETE'
        });
      } catch (err) {
        console.warn('Failed to stop stream on server:', err);
      }
    }
    
    // Clear canvas
    if (this.canvas) {
      const ctx = this.canvas.getContext('2d');
      ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    }
    
    // Reset state
    this.streamId = null;
    this.isConnected = false;
    this.stats = {
      frameCount: 0,
      clientCount: 0,
      errorCount: 0,
      lastFrameTime: null,
      secondsSinceLastFrame: 0
    };
    
    this.updateStatus('disconnected');
    this.onDisconnect();
  }

  /**
   * Get current connection status
   */
  getStatus() {
    return {
      status: this.currentStatus,
      streamId: this.streamId,
      isConnected: this.isConnected,
      stats: { ...this.stats }
    };
  }

  /**
   * Check server health
   */
  async checkServerHealth() {
    try {
      const response = await fetch(`${this.serverUrl}/health`);
      return response.ok;
    } catch {
      return false;
    }
  }
}

// Export for different environments
if (typeof module !== 'undefined' && module.exports) {
  module.exports = RTSPStreamClient;
}
if (typeof window !== 'undefined') {
  window.RTSPStreamClient = RTSPStreamClient;
}
