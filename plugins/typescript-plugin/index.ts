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
interface Customer {
    id: number;
    name: string;
    email: string;
    status: 'active' | 'inactive';
    createdAt: string;
    updatedAt: string;
    phone?: string;
    company?: string;
    tags?: string[];
}

interface Analytics {
    totalCustomers: number;
    activeCustomers: number;
    inactiveCustomers: number;
    newCustomersThisMonth: number;
    lastUpdated: string;
    averageCustomerAge: number;
}

interface CMSData {
    customers: Customer[];
    analytics: Analytics;
    lastCustomerId: number;
}

// Initialize data store
let cmsData: CMSData = {
    customers: [
        { 
            id: 1, 
            name: 'John Doe', 
            email: 'john@example.com', 
            status: 'active',
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            phone: '+1-555-0101',
            company: 'Tech Corp',
            tags: ['vip', 'enterprise']
        },
        { 
            id: 2, 
            name: 'Jane Smith', 
            email: 'jane@example.com', 
            status: 'active',
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            phone: '+1-555-0102',
            company: 'Startup Inc',
            tags: ['startup']
        },
        { 
            id: 3, 
            name: 'Bob Johnson', 
            email: 'bob@example.com', 
            status: 'inactive',
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            phone: '+1-555-0103',
            tags: ['former']
        }
    ],
    analytics: {
    totalCustomers: 3,
    activeCustomers: 2,
    inactiveCustomers: 1,
        newCustomersThisMonth: 0,
        lastUpdated: new Date().toISOString(),
        averageCustomerAge: 0
    },
    lastCustomerId: 3
};

// Data persistence functions
function saveData(): void {
    try {
        fs.writeFileSync(DATA_FILE, JSON.stringify(cmsData, null, 2));
    } catch (error) {
        console.error('Failed to save data:', error);
    }
}

function loadData(): void {
    try {
        if (fs.existsSync(DATA_FILE)) {
            const data = fs.readFileSync(DATA_FILE, 'utf8');
            cmsData = JSON.parse(data);
            updateAnalytics();
        }
    } catch (error) {
        console.error('Failed to load data:', error);
    }
}

function updateAnalytics(): void {
    const now = new Date();
    const thisMonth = now.getMonth();
    const thisYear = now.getFullYear();
    
    cmsData.analytics.totalCustomers = cmsData.customers.length;
    cmsData.analytics.activeCustomers = cmsData.customers.filter(c => c.status === 'active').length;
    cmsData.analytics.inactiveCustomers = cmsData.customers.filter(c => c.status === 'inactive').length;
    cmsData.analytics.lastUpdated = now.toISOString();
    
    // Calculate new customers this month
    cmsData.analytics.newCustomersThisMonth = cmsData.customers.filter(c => {
        const created = new Date(c.createdAt);
        return created.getMonth() === thisMonth && created.getFullYear() === thisYear;
    }).length;
    
    // Calculate average customer age (days since creation)
    const totalAge = cmsData.customers.reduce((sum, customer) => {
        const created = new Date(customer.createdAt);
        const ageInDays = Math.floor((now.getTime() - created.getTime()) / (1000 * 60 * 60 * 24));
        return sum + ageInDays;
    }, 0);
    
    cmsData.analytics.averageCustomerAge = cmsData.customers.length > 0 
        ? Math.round(totalAge / cmsData.customers.length) 
        : 0;
}

// Load data on startup
loadData();

// Start the HTTP server
app.listen(port, () => {
    console.log(`CMS Plugin HTTP server listening on port ${port}`);
});

// Handle incoming requests
app.get('/customers', (req: Request, res: Response) => {
    res.json({
        success: true,
        action: 'get_customers',
        data: cmsData.customers,
        count: cmsData.customers.length,
        timestamp: new Date().toISOString()
    });
});

app.post('/customers', (req: Request, res: Response) => {
    const { name, email, status, phone, company, tags } = req.body;

    if (!name || !email) {
        res.status(400).json({
            success: false,
            error: 'Name and email are required for add_customer action'
        });
        return;
    }

    if (cmsData.customers.find(c => c.email === email)) {
        res.status(409).json({
            success: false,
            error: 'Customer with this email already exists'
        });
        return;
    }

    const now = new Date().toISOString();
    const newCustomer: Customer = {
        id: ++cmsData.lastCustomerId,
        name: name,
        email: email,
        status: status || 'active',
        createdAt: now,
        updatedAt: now,
        phone: phone,
        company: company,
        tags: tags || []
    };

    cmsData.customers.push(newCustomer);
    updateAnalytics();
    saveData();

    res.json({
        success: true,
        action: 'add_customer',
        data: newCustomer,
        message: 'Customer added successfully',
        timestamp: now
    });
});

app.get('/analytics', (req: Request, res: Response) => {
    res.json({
        success: true,
        action: 'get_analytics',
        data: cmsData.analytics,
        timestamp: new Date().toISOString()
    });
}); 

// Main execute endpoint that the CMS calls
app.post('/execute', (req: Request, res: Response) => {
    const { action, data } = req.body;
    
    console.log(`Executing action: ${action}`, data);
    
    try {
        switch (action) {
            case 'get_customers':
                res.json({
                    success: true,
                    action: 'get_customers',
                    data: cmsData.customers,
                    count: cmsData.customers.length,
                    timestamp: new Date().toISOString()
                });
                break;
                
            case 'add_customer':
                if (!data || !data.name || !data.email) {
                    res.status(400).json({
                        success: false,
                        error: 'Name and email are required for add_customer action'
                    });
                    return;
                }
                
                if (cmsData.customers.find(c => c.email === data.email)) {
                    res.status(409).json({
                        success: false,
                        error: 'Customer with this email already exists'
                    });
                    return;
                }
                
                const now = new Date().toISOString();
                const newCustomer: Customer = {
                    id: ++cmsData.lastCustomerId,
                    name: data.name,
                    email: data.email,
                    status: data.status || 'active',
                    createdAt: now,
                    updatedAt: now,
                    phone: data.phone,
                    company: data.company,
                    tags: data.tags || []
                };
                
                cmsData.customers.push(newCustomer);
                updateAnalytics();
                saveData();
                
                res.json({
                    success: true,
                    action: 'add_customer',
                    data: newCustomer,
                    message: 'Customer added successfully',
                    timestamp: now
                });
                break;
                
            case 'get_analytics':
                res.json({
                    success: true,
                    action: 'get_analytics',
                    data: cmsData.analytics,
                    timestamp: new Date().toISOString()
                });
                break;
                
            default:
                res.status(400).json({
                    success: false,
                    error: `Unknown action: ${action}`,
                    availableActions: [
                        'get_customers', 
                        'add_customer', 
                        'get_analytics'
                    ]
                });
        }
    } catch (error: any) {
        console.error('Error executing action:', error);
        res.status(500).json({
            success: false,
            error: 'Internal server error',
            action: action
        });
    }
}); 