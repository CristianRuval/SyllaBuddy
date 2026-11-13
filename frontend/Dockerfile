# frontend/Dockerfile

# 1. Use Node.js 18 on Alpine Linux (Tiny image)
FROM node:20-alpine

# 2. Set working directory
WORKDIR /app

# 3. Copy package files first (better caching)
COPY package.json package-lock.json ./

# 4. Install dependencies
RUN npm ci

# 5. Copy the rest of the frontend code
COPY . .

# 6. Build the Next.js application
RUN npm run build

# 7. Expose the port Next.js runs on
EXPOSE 3000

# 8. Start the app
CMD ["npm", "start"]