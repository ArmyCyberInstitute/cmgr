FROM ubuntu:20.04

RUN apt-get update
RUN apt-get -y install php build-essential
RUN groupadd -r php && useradd -r -d /app -g php php

# End of shared layers for all php challenges

COPY Dockerfile packages.txt* .
RUN if [ -f packages.txt ]; then xargs -a packages.txt sudo apt install -y; fi

COPY --chown=php:php . /app

# End of share layers for all builds of the same php challenge

ARG FLAG
ARG SEED

RUN install -d -m 0700 /challenge && \
    echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json

USER php:php

RUN find /app -name \( -name *.php -o -name *.txt -o -name *.html \) \
              -exec sed -i -e "s|{{flag}}|$FLAG|g"                   \
                           -e "s|{{seed}}|$SEED|g"                   \
                        {} \;

WORKDIR /app
CMD php -S 0.0.0.0:8000 -t /app

EXPOSE 8000
# PUBLISH 8000 AS http
