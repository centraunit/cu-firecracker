FROM node:18-alpine

WORKDIR /plugin

# Copy package files
COPY package*.json ./

# Install all dependencies (including dev for TypeScript)
RUN npm install

# Copy source code
COPY . .

# Build TypeScript
RUN npm run build

# Remove dev dependencies to reduce image size
RUN npm prune --omit=dev

EXPOSE 8080

CMD ["node", "index.js"] 