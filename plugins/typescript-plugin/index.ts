import * as fs from 'fs';
import * as path from 'path';
import express from 'express';
import { Request, Response } from 'express';

const app = express();
const port = 80;

// Enable JSON parsing
app.use(express.json());

// Data file path
const DATA_FILE = '/tmp/cms_data.json';

// Interface definitions
interface ContentItem {
    id: number;
    title: string;
    content: string;
    status: 'draft' | 'published';
    seo_score?: number;
    created_at: string;
    updated_at?: string;
}

interface MediaItem {
    id: number;
    filename: string;
    type: string;
    size: number;
    optimized: boolean;
    url: string;
    created_at: string;
}

interface CMSData {
    content: ContentItem[];
    media: MediaItem[];
    last_updated: string;
}

// Helper function to read data
function readData(): CMSData {
    try {
        if (fs.existsSync(DATA_FILE)) {
            const data = fs.readFileSync(DATA_FILE, 'utf8');
            return JSON.parse(data);
        }
    } catch (error) {
        console.error('Error reading data:', error);
    }
    
    return {
        content: [
            { id: 1, title: 'Welcome', content: 'Welcome to TypeScript CMS!', status: 'published', created_at: new Date().toISOString() }
        ],
        media: [],
        last_updated: new Date().toISOString()
    };
}

// Helper function to write data
function writeData(data: CMSData): void {
    try {
        data.last_updated = new Date().toISOString();
        fs.writeFileSync(DATA_FILE, JSON.stringify(data, null, 2));
    } catch (error) {
        console.error('Error writing data:', error);
    }
}

// Health check endpoint
app.get('/health', (req: Request, res: Response) => {
    res.status(200).json({ status: 'healthy' });
});

// Discovery endpoint
app.get('/actions', (req: Request, res: Response) => {
    res.status(200).json({
        "content_management": {
            "name": "Content Manager",
            "description": "Manages CMS content and pages",
            "hooks": ["content.create", "content.update", "content.delete"],
            "method": "POST",
            "endpoint": "/actions/content",
            "priority": 10
        },
        "media_processing": {
            "name": "Media Processor",
            "description": "Handles media uploads and processing",
            "hooks": ["media.upload", "media.process", "media.optimize"],
            "method": "POST",
            "endpoint": "/actions/media",
            "priority": 7
        },
        "seo_optimization": {
            "name": "SEO Optimizer",
            "description": "Optimizes content for search engines",
            "hooks": ["seo.analyze", "seo.optimize"],
            "method": "POST",
            "endpoint": "/actions/seo",
            "priority": 5
        }
    });
}); 

// Content management endpoint
app.post('/actions/content', (req: Request, res: Response) => {
    const { action, ...params } = req.body;
    const data = readData();
    
    try {
        switch (action) {
            case 'create':
                const newContent: ContentItem = {
                    id: data.content.length + 1,
                    title: params.title || 'Untitled',
                    content: params.content || '',
                    status: params.status || 'draft',
                    created_at: new Date().toISOString()
                };
                data.content.push(newContent);
                writeData(data);
                res.json({ success: true, content: newContent });
                break;
                
            case 'update':
                const contentId = parseInt(params.id);
                const contentIndex = data.content.findIndex(c => c.id === contentId);
                if (contentIndex !== -1) {
                    data.content[contentIndex] = { ...data.content[contentIndex], ...params, updated_at: new Date().toISOString() };
                    writeData(data);
                    res.json({ success: true, content: data.content[contentIndex] });
                } else {
                    res.status(404).json({ success: false, error: 'Content not found' });
                }
                break;

            case 'delete':
                const deleteId = parseInt(params.id);
                const initialLength = data.content.length;
                data.content = data.content.filter(c => c.id !== deleteId);
                if (data.content.length < initialLength) {
                    writeData(data);
                    res.json({ success: true, message: 'Content deleted' });
                } else {
                    res.status(404).json({ success: false, error: 'Content not found' });
                }
                break;

            default:
                res.json({ success: true, content: data.content, total: data.content.length });
        }
    } catch (error) {
        res.status(500).json({ success: false, error: 'Internal server error' });
    }
});

// Media processing endpoint
app.post('/actions/media', (req: Request, res: Response) => {
    const { action, ...params } = req.body;
    const data = readData();

    try {
        switch (action) {
            case 'upload':
                const newMedia: MediaItem = {
                    id: data.media.length + 1,
                    filename: params.filename || 'unknown.jpg',
                    type: params.type || 'image/jpeg',
                    size: params.size || 0,
                    optimized: false,
                    url: `/media/${params.filename}`,
                    created_at: new Date().toISOString()
                };
                data.media.push(newMedia);
                writeData(data);
                res.json({ success: true, media: newMedia });
                break;

            case 'optimize':
                const mediaId = parseInt(params.id);
                const mediaIndex = data.media.findIndex(m => m.id === mediaId);
                if (mediaIndex !== -1) {
                    data.media[mediaIndex].optimized = true;
                    data.media[mediaIndex].size = Math.floor(data.media[mediaIndex].size * 0.7); // Simulate optimization
                    writeData(data);
                    res.json({ success: true, media: data.media[mediaIndex] });
                } else {
                    res.status(404).json({ success: false, error: 'Media not found' });
                }
                break;

            default:
                res.json({ success: true, media: data.media, total: data.media.length });
        }
    } catch (error) {
        res.status(500).json({ success: false, error: 'Internal server error' });
    }
});

// SEO optimization endpoint
app.post('/actions/seo', (req: Request, res: Response) => {
    const { action, ...params } = req.body;
    const data = readData();

    try {
        switch (action) {
            case 'analyze':
                const contentId = parseInt(params.id);
                const content = data.content.find(c => c.id === contentId);
                if (content) {
                    const score = Math.floor(Math.random() * 40) + 60; // Random score 60-100
                res.json({
                    success: true,
                        seo_analysis: {
                            content_id: contentId,
                            score: score,
                            recommendations: ['Add meta description', 'Optimize images', 'Improve heading structure']
                        }
                    });
                } else {
                    res.status(404).json({ success: false, error: 'Content not found' });
                }
                break;
                
            case 'optimize':
                const optimizeId = parseInt(params.id);
                const contentIndex = data.content.findIndex(c => c.id === optimizeId);
                if (contentIndex !== -1) {
                    data.content[contentIndex].seo_score = 95; // Set high score after optimization
                    writeData(data);
                    res.json({ success: true, content: data.content[contentIndex] });
                } else {
                    res.status(404).json({ success: false, error: 'Content not found' });
                }
                break;
                
            default:
                res.json({ success: false, error: 'Unknown SEO action' });
        }
    } catch (error) {
        res.status(500).json({ success: false, error: 'Internal server error' });
        }
});

// Root endpoint
app.get('/', (req: Request, res: Response) => {
    res.json({
        message: 'TypeScript CMS Plugin',
        version: '1.0.0',
        endpoints: ['/health', '/actions', '/actions/content', '/actions/media', '/actions/seo'],
        timestamp: new Date().toISOString()
    });
});

// Start the server
app.listen(port, () => {
    console.log(`TypeScript CMS Plugin HTTP server listening on port ${port}`);
}); 