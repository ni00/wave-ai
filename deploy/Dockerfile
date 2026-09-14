FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci --ignore-scripts
COPY web .
COPY sdks/typescript/src /src/sdks/typescript/src
RUN npm run build

FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/platform/webui/static ./internal/platform/webui/static
RUN CGO_ENABLED=0 go build -trimpath -tags=nomsgpack -o /out/wave ./cmd/wave

FROM alpine:3.24.1
RUN apk add --no-cache ca-certificates bash
COPY --from=build /out/wave /usr/local/bin/wave
ENV WAVE_DATA_DIR=/var/lib/wave
EXPOSE 8080
ENTRYPOINT ["wave"]
CMD ["serve", "-role", "all"]
