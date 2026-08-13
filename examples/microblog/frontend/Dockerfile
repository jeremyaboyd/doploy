# Built on the droplet by doploy, not pulled from a registry.

FROM node:20-alpine AS build

WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund

COPY vite.config.js index.html ./
COPY src ./src
RUN npm run build

# --- serve -------------------------------------------------------------------

FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80
