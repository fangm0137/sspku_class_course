FROM node:24-alpine AS build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM nginx:stable-alpine
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist/ /usr/share/nginx/html/
# Swagger UI 静态文件随镜像分发，/swagger 不依赖外网。
COPY deploy/swagger/ /usr/share/nginx/swagger/
EXPOSE 80
