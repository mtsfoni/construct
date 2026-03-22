FROM construct-stack-base:latest

# Install ICU libraries for .NET globalization support (Debian Bookworm ships libicu72)
RUN apt-get update && apt-get install -y --no-install-recommends libicu72 && rm -rf /var/lib/apt/lists/*

# Install .NET SDK versions: 8.0 (LTS), 9.0 (STS), 10.0 (current)
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 8.0 --install-dir /usr/local/dotnet
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 9.0 --install-dir /usr/local/dotnet
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 10.0 --install-dir /usr/local/dotnet

ENV PATH=/usr/local/dotnet:/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV DOTNET_ROOT=/usr/local/dotnet
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1

# Ensure dotnet is on PATH even in login shells.
RUN echo 'export PATH="/usr/local/dotnet:/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"' \
    > /etc/profile.d/construct-path.sh

LABEL io.construct.stack=dotnet
