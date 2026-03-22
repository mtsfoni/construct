FROM construct-stack-base:latest

# Install rbenv dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    libssl-dev \
    libreadline-dev \
    zlib1g-dev \
    libyaml-dev \
    libgdbm-dev \
    libncurses5-dev \
    libffi-dev \
    && rm -rf /var/lib/apt/lists/*

# Install rbenv and ruby-build
RUN git clone https://github.com/rbenv/rbenv.git /usr/local/rbenv \
    && git clone https://github.com/rbenv/ruby-build.git /usr/local/rbenv/plugins/ruby-build

ENV RBENV_ROOT=/usr/local/rbenv
ENV PATH=/usr/local/rbenv/bin:/usr/local/rbenv/shims:/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# Ensure rbenv is on PATH even in login shells.
RUN echo 'export PATH="/usr/local/rbenv/bin:/usr/local/rbenv/shims:/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"' \
    > /etc/profile.d/construct-path.sh

# Install latest stable Ruby
RUN rbenv install $(rbenv install -l | grep -v - | tail -1) \
    && rbenv global $(rbenv install -l | grep -v - | tail -1)

RUN gem install bundler

LABEL io.construct.stack=ruby
