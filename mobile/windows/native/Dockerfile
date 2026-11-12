# Build tools only. No account state, credentials, or live server config enter it.
FROM ubuntu:22.04
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends build-essential autoconf automake texinfo ca-certificates python3 zstd git pkg-config nsis libglib2.0-bin shared-mime-info && rm -rf /var/lib/apt/lists/*
COPY llvm-mingw.tar.xz /tmp/llvm-mingw.tar.xz
ARG LLVM_SHA256
RUN echo "$LLVM_SHA256  /tmp/llvm-mingw.tar.xz" | sha256sum -c - && mkdir /opt/llvm-mingw && tar -xf /tmp/llvm-mingw.tar.xz --strip-components=1 -C /opt/llvm-mingw && rm /tmp/llvm-mingw.tar.xz
ENV PATH=/opt/llvm-mingw/bin:$PATH
WORKDIR /work
