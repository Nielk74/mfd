FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mfd ./cmd/mfd

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 mfd
COPY --from=build /mfd /usr/local/bin/mfd
USER mfd
EXPOSE 8080
ENTRYPOINT ["mfd"]
