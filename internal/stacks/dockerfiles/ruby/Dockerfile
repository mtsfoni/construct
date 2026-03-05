FROM construct-base

USER root

RUN apt-get update && apt-get install -y \
    ruby-full \
    ruby-dev \
    build-essential \
    zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

RUN gem install bundler jekyll --no-document

USER agent
