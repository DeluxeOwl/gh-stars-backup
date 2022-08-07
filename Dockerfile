FROM golang:alpine

WORKDIR /
COPY . .

RUN apk add git

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /gh-stars-backup -a -ldflags='-s -w' main.go

ENTRYPOINT ["/gh-stars-backup"]