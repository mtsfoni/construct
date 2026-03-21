FROM construct-stack-base:latest

# Install .NET SDK (latest LTS)
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel LTS --install-dir /usr/local/dotnet

ENV PATH=/usr/local/dotnet:$PATH
ENV DOTNET_ROOT=/usr/local/dotnet

LABEL io.construct.stack=dotnet
