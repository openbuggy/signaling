# syntax=docker/dockerfile:1

FROM golang:1.19

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN go build -v -o ./app

CMD ["./app"]
