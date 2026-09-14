# Build context is the repository root (see docker-compose.yml): the app
# inlines the markdown docs from /docs at build time, so they must be present
# next to apps/web in the image.
FROM node:22-alpine AS build
WORKDIR /build

COPY apps/web/package.json apps/web/package-lock.json* ./apps/web/
RUN cd apps/web && npm ci || npm install

COPY apps/web ./apps/web
COPY docs ./docs

WORKDIR /build/apps/web
ARG VITE_API_BASE_URL=http://localhost:8080
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /build/apps/web/dist /usr/share/nginx/html
# SPA routing: every unknown path falls back to index.html.
RUN printf 'server {\n\
  listen 5173;\n\
  root /usr/share/nginx/html;\n\
  index index.html;\n\
  location / { try_files $uri $uri/ /index.html; }\n\
}\n' > /etc/nginx/conf.d/default.conf
EXPOSE 5173
