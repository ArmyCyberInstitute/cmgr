FROM ubuntu:20.04 AS base
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    php
RUN groupadd -r php && useradd -r -d /app -g php php

# End of shared layers for all php challenges

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then apt-get update && xargs -a packages.txt apt-get install -y; fi

COPY --chown=php:php . /app

# End of share layers for all builds of the same php challenge
FROM base AS challenge

ARG FLAG
ARG SEED

RUN install -d -m 0700 /challenge && \
    echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json

USER php:php

RUN find /app \( -name *.php -o -name *.txt -o -name *.html \) \
              -exec sed -i -e "s|{{flag}}|$FLAG|g"             \
                           -e "s|{{seed}}|$SEED|g"             \
                        {} \;

WORKDIR /app
CMD php -S 0.0.0.0:5000

EXPOSE 5000
# PUBLISH 5000 AS http
