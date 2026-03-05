FROM construct-base

USER root

ARG TARGETARCH=amd64
RUN curl -fsSL "https://go.dev/dl/go1.24.0.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz

ENV PATH="/usr/local/go/bin:${PATH}"

USER agent
