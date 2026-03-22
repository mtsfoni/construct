FROM construct-stack-base:latest

# Install Go
ENV GO_VERSION=1.23.4
RUN curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xzf -

ENV PATH=/usr/local/go/bin:/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV GOPATH=/agent/lib/go
ENV GOBIN=/agent/lib/go/bin

# Ensure go is on PATH even in login shells.
RUN echo 'export PATH="/usr/local/go/bin:/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"' \
    > /etc/profile.d/construct-path.sh

LABEL io.construct.stack=go
