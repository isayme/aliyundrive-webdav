FROM golang:1.17.8-alpine as builder
WORKDIR /app

ARG APP_NAME
ENV APP_NAME ${APP_NAME}
ARG APP_VERSION
ENV APP_VERSION ${APP_VERSION}

COPY . .
RUN mkdir -p ./dist  \
  && GO111MODULE=on go mod download \
  && go build -ldflags "-X github.com/isayme/aliyundrive-webdav/util.Name=${APP_NAME} \
  -X github.com/isayme/aliyundrive-webdav/util.Version=${APP_VERSION}" \
  -o ./dist/aliyundrive-webdav main.go

FROM alpine
WORKDIR /app

ARG APP_NAME
ENV APP_NAME ${APP_NAME}
ARG APP_VERSION
ENV APP_VERSION ${APP_VERSION}

COPY --from=builder /app/dist/aliyundrive-webdav ./

CMD ["/app/aliyundrive-webdav"]
