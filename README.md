# 阿里云盘 webdav 服务器

[![Docker Image Version (latest semver)](https://img.shields.io/docker/v/isayme/aliyundrive-webdav?sort=semver&style=flat-square)](https://hub.docker.com/r/isayme/aliyundrive-webdav)
![Docker Image Size (latest semver)](https://img.shields.io/docker/image-size/isayme/aliyundrive-webdav?sort=semver&style=flat-square)
![Docker Pulls](https://img.shields.io/docker/pulls/isayme/aliyundrive-webdav?style=flat-square)

# 特性支持

- [x] 文件浏览
- [x] 文件移动
- [x] 文件重命名
- [x] 新建文件夹
- [x] 文件删除(放到阿里云盘回收站)
- [x] 文件上传

# 如何使用

## Docker Compose

```
version: '3'

services:
  aliyundrive-webdav:
    container_name: aliyundrive-webdav
    image: isayme/aliyundrive-webdav:0.2.1
    volumes:
      - ./path/to/data:/data
    environment:
      CONF_FILE_PATH: '/data/config.yaml'
    ports:
      - '4918:8080'
```
