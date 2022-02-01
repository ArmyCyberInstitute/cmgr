FROM node:16 AS base

RUN groupadd -r app && useradd -r -d /app -g app app

# End of shared layers for all node challenges

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then apt-get update && xargs -a packages.txt apt-get install -y; fi

COPY --chown=app:app . /app

WORKDIR /app
USER app:app

ENV PORT=5000
RUN npm ci --only=production

# End of share layers for all builds of the same node challenge
FROM base AS challenge

ARG FLAG
ARG SEED

USER root:root
RUN install -d -m 0700 /challenge && \
    echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json

RUN find /app -type f ! -name Dockerfile           \
              -exec sed -i -e "s|{{flag}}|$FLAG|g" \
                           -e "s|{{seed}}|$SEED|g" \
                        {} \;

USER app:app
CMD npm start

EXPOSE 5000
# PUBLISH 5000 AS http
