FROM ubuntu:20.04 AS base
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    build-essential

RUN groupadd -r app && useradd -r -d /app -g app app
RUN install -d -m 0700 /challenge

ENV PORT=5000
# End of shared layers for all flask challenges

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then apt-get update && xargs -a packages.txt apt-get install -y; fi

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
CMD make run

EXPOSE 5000
# PUBLISH 5000 AS service
