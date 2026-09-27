FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /pureblog-go .

FROM alpine:3
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /pureblog-go /usr/local/bin/pureblog-go
VOLUME /site
EXPOSE 8080
ENTRYPOINT ["pureblog-go", "-dir", "/site", "-addr", ":8080"]
