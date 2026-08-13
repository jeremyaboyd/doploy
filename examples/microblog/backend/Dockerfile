# Built on the droplet by doploy, not pulled from a registry.

FROM node:20-alpine

ENV NODE_ENV=production
WORKDIR /app

# Dependencies first so the layer is reused when only source changes.
COPY package.json package-lock.json* ./
RUN npm install --omit=dev --no-audit --no-fund

COPY src ./src
COPY healthcheck.js ./

# The uploads directory is a bind mount from the droplet's block volume. It is
# created here so the image still runs without one.
RUN mkdir -p /var/lib/microblog/uploads \
    && chown -R node:node /app /var/lib/microblog

USER node
EXPOSE 8080

CMD ["node", "src/index.js"]
