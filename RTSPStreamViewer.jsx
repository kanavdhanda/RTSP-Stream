import React, { useState, useEffect, useRef } from 'react';

/**
 * React component for displaying RTSP streams
 * This can be used in your Wails React frontend
 */
const RTSPStreamViewer = ({ 
    serverUrl = 'ws://localhost:8080', 
    streamId = 'camera1', 
    rtspUrl = '', 
    width = 640, 
    height = 480,
    autoStart = false 
}) => {
    const canvasRef = useRef(null);
    const clientRef = useRef(null);
    
    const [isConnected, setIsConnected] = useState(false);
    const [isStreamActive, setIsStreamActive] = useState(false);
    const [error, setError] = useState(null);
    const [stats, setStats] = useState({
        framesReceived: 0,
        bytesReceived: 0,
        averageFps: 0,
        lastFrameTime: null
    });
    const [serverStats, setServerStats] = useState(null);

    // Initialize client
    useEffect(() => {
        // Import the RTSPStreamClient class
        const RTSPStreamClient = window.RTSPStreamClient;
        if (!RTSPStreamClient) {
            setError('RTSPStreamClient not found. Make sure js_client.js is loaded.');
            return;
        }

        const client = new RTSPStreamClient(serverUrl, streamId);
        clientRef.current = client;

        // Setup canvas
        if (canvasRef.current) {
            client.setupCanvas(canvasRef.current);
        }

        // Setup event handlers
        client.onConnect(() => {
            setIsConnected(true);
            setError(null);
            console.log(`Connected to stream ${streamId}`);
        });

        client.onDisconnect(() => {
            setIsConnected(false);
            console.log(`Disconnected from stream ${streamId}`);
        });

        client.onError((error) => {
            setError(error);
            console.error(`Stream error: ${error}`);
        });

        client.onFrame(() => {
            setStats(client.getStats());
        });

        // Auto-start if enabled
        if (autoStart && rtspUrl) {
            startStream();
        }

        return () => {
            if (clientRef.current) {
                clientRef.current.disconnect();
                stopStreamOnServer();
            }
        };
    }, [serverUrl, streamId]);

    const startStream = async () => {
        if (!clientRef.current || !rtspUrl) {
            setError('Client not initialized or RTSP URL not provided');
            return;
        }

        setError(null);
        
        try {
            // Start stream on server
            const success = await clientRef.current.startStream(rtspUrl, width, height);
            if (success) {
                setIsStreamActive(true);
                // Wait a moment for stream to initialize
                setTimeout(() => {
                    clientRef.current.connect();
                }, 1000);
            } else {
                setError('Failed to start stream on server');
            }
        } catch (err) {
            setError(`Failed to start stream: ${err.message}`);
        }
    };

    const stopStream = async () => {
        if (!clientRef.current) return;

        try {
            clientRef.current.disconnect();
            await stopStreamOnServer();
            setIsStreamActive(false);
            setIsConnected(false);
        } catch (err) {
            setError(`Failed to stop stream: ${err.message}`);
        }
    };

    const stopStreamOnServer = async () => {
        if (!clientRef.current) return;
        
        try {
            await clientRef.current.stopStream();
        } catch (err) {
            console.warn('Error stopping stream on server:', err);
        }
    };

    const refreshServerStats = async () => {
        if (!clientRef.current) return;
        
        try {
            const stats = await clientRef.current.getStreamStats();
            setServerStats(stats);
        } catch (err) {
            console.error('Failed to get server stats:', err);
        }
    };

    const resetStats = () => {
        if (clientRef.current) {
            clientRef.current.resetStats();
            setStats({
                framesReceived: 0,
                bytesReceived: 0,
                averageFps: 0,
                lastFrameTime: null
            });
        }
    };

    const formatBytes = (bytes) => {
        if (bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    };

    const formatTime = (timestamp) => {
        if (!timestamp) return 'Never';
        return new Date(timestamp).toLocaleTimeString();
    };

    return (
        <div className="rtsp-stream-viewer">
            <div className="stream-controls">
                <h3>RTSP Stream: {streamId}</h3>
                
                <div className="control-buttons">
                    {!isStreamActive ? (
                        <button 
                            onClick={startStream} 
                            disabled={!rtspUrl}
                            className="btn btn-primary"
                        >
                            Start Stream
                        </button>
                    ) : (
                        <button 
                            onClick={stopStream}
                            className="btn btn-danger"
                        >
                            Stop Stream
                        </button>
                    )}
                    
                    <button 
                        onClick={refreshServerStats}
                        className="btn btn-secondary"
                    >
                        Refresh Stats
                    </button>
                    
                    <button 
                        onClick={resetStats}
                        className="btn btn-secondary"
                    >
                        Reset Client Stats
                    </button>
                </div>

                <div className="status-indicators">
                    <span className={`status ${isStreamActive ? 'active' : 'inactive'}`}>
                        Stream: {isStreamActive ? 'Active' : 'Inactive'}
                    </span>
                    <span className={`status ${isConnected ? 'connected' : 'disconnected'}`}>
                        WebSocket: {isConnected ? 'Connected' : 'Disconnected'}
                    </span>
                </div>

                {error && (
                    <div className="error-message">
                        Error: {error}
                    </div>
                )}
            </div>

            <div className="stream-display">
                <canvas
                    ref={canvasRef}
                    width={width}
                    height={height}
                    style={{
                        border: '1px solid #ccc',
                        maxWidth: '100%',
                        height: 'auto'
                    }}
                />
            </div>

            <div className="stats-panels">
                <div className="stats-panel">
                    <h4>Client Statistics</h4>
                    <div className="stats-grid">
                        <div className="stat-item">
                            <label>Frames Received:</label>
                            <span>{stats.framesReceived}</span>
                        </div>
                        <div className="stat-item">
                            <label>Data Received:</label>
                            <span>{formatBytes(stats.bytesReceived)}</span>
                        </div>
                        <div className="stat-item">
                            <label>Average FPS:</label>
                            <span>{stats.averageFps.toFixed(2)}</span>
                        </div>
                        <div className="stat-item">
                            <label>Last Frame:</label>
                            <span>{formatTime(stats.lastFrameTime)}</span>
                        </div>
                    </div>
                </div>

                {serverStats && (
                    <div className="stats-panel">
                        <h4>Server Statistics</h4>
                        <div className="stats-grid">
                            <div className="stat-item">
                                <label>Stream ID:</label>
                                <span>{serverStats.stream_id}</span>
                            </div>
                            <div className="stat-item">
                                <label>RTSP URL:</label>
                                <span title={serverStats.rtsp_url}>
                                    {serverStats.rtsp_url?.substring(0, 40)}...
                                </span>
                            </div>
                            <div className="stat-item">
                                <label>Running:</label>
                                <span>{serverStats.is_running ? 'Yes' : 'No'}</span>
                            </div>
                            <div className="stat-item">
                                <label>Client Count:</label>
                                <span>{serverStats.client_count}</span>
                            </div>
                            <div className="stat-item">
                                <label>Frame Count:</label>
                                <span>{serverStats.frame_count}</span>
                            </div>
                            <div className="stat-item">
                                <label>Buffer Size:</label>
                                <span>{serverStats.buffer_size}</span>
                            </div>
                        </div>
                    </div>
                )}
            </div>

            <style jsx>{`
                .rtsp-stream-viewer {
                    padding: 20px;
                    max-width: 1200px;
                    margin: 0 auto;
                }

                .stream-controls {
                    margin-bottom: 20px;
                }

                .control-buttons {
                    margin: 10px 0;
                }

                .btn {
                    margin-right: 10px;
                    padding: 8px 16px;
                    border: none;
                    border-radius: 4px;
                    cursor: pointer;
                    font-size: 14px;
                }

                .btn-primary {
                    background-color: #007bff;
                    color: white;
                }

                .btn-danger {
                    background-color: #dc3545;
                    color: white;
                }

                .btn-secondary {
                    background-color: #6c757d;
                    color: white;
                }

                .btn:disabled {
                    opacity: 0.6;
                    cursor: not-allowed;
                }

                .status-indicators {
                    margin: 10px 0;
                }

                .status {
                    display: inline-block;
                    padding: 4px 8px;
                    border-radius: 4px;
                    margin-right: 10px;
                    font-size: 12px;
                    font-weight: bold;
                }

                .status.active, .status.connected {
                    background-color: #d4edda;
                    color: #155724;
                }

                .status.inactive, .status.disconnected {
                    background-color: #f8d7da;
                    color: #721c24;
                }

                .error-message {
                    background-color: #f8d7da;
                    color: #721c24;
                    padding: 10px;
                    border-radius: 4px;
                    margin: 10px 0;
                }

                .stream-display {
                    text-align: center;
                    margin-bottom: 20px;
                }

                .stats-panels {
                    display: grid;
                    grid-template-columns: 1fr 1fr;
                    gap: 20px;
                }

                .stats-panel {
                    background-color: #f8f9fa;
                    padding: 15px;
                    border-radius: 4px;
                }

                .stats-grid {
                    display: grid;
                    gap: 10px;
                }

                .stat-item {
                    display: flex;
                    justify-content: space-between;
                }

                .stat-item label {
                    font-weight: bold;
                }

                @media (max-width: 768px) {
                    .stats-panels {
                        grid-template-columns: 1fr;
                    }
                }
            `}</style>
        </div>
    );
};

export default RTSPStreamViewer;
