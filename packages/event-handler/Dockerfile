FROM golang:1.21 AS builder

WORKDIR /app

COPY ./src .
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

RUN go build -a -installsuffix cgo -o main -tags timetzdata ./cmd

# ---
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/main .

EXPOSE 8080
ENTRYPOINT ["/app/main"]
CMD ["./main"]
