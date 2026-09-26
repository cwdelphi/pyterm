FROM golang:1.24-bookworm
RUN if [ -f /etc/apt/sources.list.d/debian.sources ]; then       sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources;     else       sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list;     fi
ENV GOPROXY=https://goproxy.cn,direct
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
