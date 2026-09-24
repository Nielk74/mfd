FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG MFD_REVISION=development
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/Nielk74/mfd/internal/httpapi.Revision=${MFD_REVISION}" -o /mfd ./cmd/mfd

FROM alpine:3.23
ARG MFD_REVISION=development
LABEL org.opencontainers.image.revision=${MFD_REVISION}
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 mfd
COPY --from=build /mfd /usr/local/bin/mfd
USER mfd
EXPOSE 8080
ENTRYPOINT ["mfd"]
