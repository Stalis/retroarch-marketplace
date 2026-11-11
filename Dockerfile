# Stage 1: Build Web Admin
FROM node:22-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
COPY internal/store/known-platforms.json /app/internal/store/known-platforms.json
RUN npm run build

# Stage 2: Build Go Server with embedded Web
FROM golang:1.27-alpine AS server-builder
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -tags release -ldflags="-s -w -X main.Version=${VERSION}" -o /content-catalog .

# Stage 3: Minimal Runtime
FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /data
COPY --from=server-builder /content-catalog /usr/local/bin/content-catalog

ENV RETRO_LISTEN="0.0.0.0:5050" \
    RETRO_PACKAGES="/data/packages"

EXPOSE 5050
VOLUME ["/data"]

ENTRYPOINT ["content-catalog"]
CMD ["serve"]
