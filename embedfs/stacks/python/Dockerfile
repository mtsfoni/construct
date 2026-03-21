FROM construct-stack-base:latest

# Install pyenv dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    libssl-dev \
    libffi-dev \
    libbz2-dev \
    libreadline-dev \
    libsqlite3-dev \
    libncurses5-dev \
    libncursesw5-dev \
    xz-utils \
    tk-dev \
    liblzma-dev \
    && rm -rf /var/lib/apt/lists/*

# Install pipx, poetry, uv via pip
RUN pip3 install --break-system-packages pipx poetry uv

LABEL io.construct.stack=python
