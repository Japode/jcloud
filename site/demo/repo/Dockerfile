FROM node:22-alpine
WORKDIR /app
COPY . .
RUN npm ci --omit=dev
VOLUME /data
EXPOSE 3000
HEALTHCHECK --interval=30s CMD wget -qO- http://127.0.0.1:3000/healthz || exit 1
CMD ["node", "server.js"]
