FROM construct-stack-base:latest

# Install multiple .NET SDK versions (current LTS, previous LTS, current STS)
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 8.0 --install-dir /usr/local/dotnet
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 6.0 --install-dir /usr/local/dotnet
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 9.0 --install-dir /usr/local/dotnet

ENV PATH=/usr/local/dotnet:$PATH
ENV DOTNET_ROOT=/usr/local/dotnet

LABEL io.construct.stack=dotnet-big
