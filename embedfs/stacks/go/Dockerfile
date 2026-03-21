FROM construct-stack-base:latest

# Install Go
ENV GO_VERSION=1.23.4
RUN curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xzf -

ENV PATH=/usr/local/go/bin:$PATH
ENV GOPATH=/agent/lib/go
ENV GOBIN=/agent/lib/go/bin

LABEL io.construct.stack=go
