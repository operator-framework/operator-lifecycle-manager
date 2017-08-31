FROM golang:1.9.0

WORKDIR /go/src/app

EXPOSE 8080
CMD ["go-wrapper", "run"] # ["app"]

COPY . .

RUN go-wrapper download   # "go get -d -v ./..."
RUN go-wrapper install    # "go install -v ./..."
