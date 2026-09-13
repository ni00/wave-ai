FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -tags=nomsgpack -o /out/wave ./cmd/wave

FROM alpine:3.24.1
RUN apk add --no-cache ca-certificates bash
COPY --from=build /out/wave /usr/local/bin/wave
ENV WAVE_DATA_DIR=/var/lib/wave
EXPOSE 8080
ENTRYPOINT ["wave"]
CMD ["serve", "-role", "all"]
