#!/usr/bin/env python3

from flask import Flask, request, jsonify
from datetime import datetime
import os

app = Flask(__name__)

@app.route('/health', methods=['GET'])
def health():
    return jsonify({
        'status': 'healthy',
        'plugin': 'python-cms-plugin',
        'version': '1.0.0',
        'timestamp': datetime.now().isoformat()
    })

@app.route('/', methods=['GET'])
def main():
    return jsonify({
        'message': 'Hello from Python CMS Plugin!',
        'plugin': 'python-cms-plugin',
        'version': '1.0.0',
        'timestamp': datetime.now().isoformat(),
        'request': {
            'method': request.method,
            'path': request.path,
            'headers': dict(request.headers)
        }
    })

@app.route('/execute', methods=['POST'])
def execute():
    data = request.get_json() or {}
    action = data.get('action', 'default')
    
    return jsonify({
        'status': 'success',
        'action': action,
        'data': data,
        'result': 'Python plugin executed successfully',
        'timestamp': datetime.now().isoformat()
    })

@app.route('/api/customers', methods=['GET'])
def get_customers():
    return jsonify({
        'customers': [
            {'id': 1, 'name': 'Alice Johnson', 'email': 'alice@example.com'},
            {'id': 2, 'name': 'Bob Wilson', 'email': 'bob@example.com'}
        ],
        'total': 2,
        'plugin': 'python-cms-plugin'
    })

@app.route('/api/customers', methods=['POST'])
def create_customer():
    data = request.get_json() or {}
    name = data.get('name', 'Unknown')
    email = data.get('email', 'unknown@example.com')
    
    return jsonify({
        'status': 'success',
        'message': 'Customer created via Python plugin',
        'customer': {'id': 3, 'name': name, 'email': email},
        'plugin': 'python-cms-plugin'
    })

@app.route('/api/analytics', methods=['GET'])
def analytics():
    return jsonify({
        'metrics': {
            'total_customers': 150,
            'active_deals': 25,
            'revenue': 50000
        },
        'plugin': 'python-cms-plugin'
    })

if __name__ == '__main__':
    port = int(os.environ.get('PORT', 8080))
    app.run(host='0.0.0.0', port=port, debug=False) 