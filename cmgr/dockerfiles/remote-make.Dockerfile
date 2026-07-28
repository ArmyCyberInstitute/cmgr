FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90 AS base
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    socat \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -r app && useradd -r -d /app -g app app
RUN install -d -m 0700 /challenge
# End of shared layers for all flask challenges

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then \
        apt-get update \
        && xargs -r -a packages.txt apt-get install -y --no-install-recommends \
        && rm -rf /var/lib/apt/lists/*; \
    fi

COPY . /app
WORKDIR /app

# End of share layers for all builds of the same flask challenge
FROM base AS challenge

ARG FLAG_FORMAT
ARG FLAG
ARG SEED

RUN make main
RUN make artifacts.tar.gz && mv artifacts.tar.gz /challenge || true
RUN make metadata.json && mv metadata.json /challenge
RUN make sanitize || true

RUN chown -R app:app /app

USER app:app
CMD socat TCP4-LISTEN:5000,reuseaddr,fork exec:'make run'

EXPOSE 5000
# PUBLISH 5000 AS socat
