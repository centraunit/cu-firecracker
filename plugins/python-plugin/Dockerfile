FROM python:3.11-slim

WORKDIR /plugin

# Copy requirements first for better caching
COPY requirements.txt .

# Install dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy source code
COPY . .

EXPOSE 8080

CMD ["python3", "main.py"] 