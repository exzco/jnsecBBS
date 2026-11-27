FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --registry=https://registry.npmmirror.com || npm install --legacy-peer-deps --registry=https://registry.npmmirror.com
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/internal/frontend/dist internal/frontend/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bbs ./cmd

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /bbs /app/bbs
COPY config.yaml /app/config.yaml
VOLUME ["/app/data"]
EXPOSE 8080
ENTRYPOINT ["/app/bbs"]
CMD ["serve", "-c", "/app/config.yaml"]
