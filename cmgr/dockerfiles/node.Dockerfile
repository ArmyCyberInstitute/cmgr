FROM node:12

RUN groupadd -r app && useradd -r -d /app -g app app

# End of shared layers for all node challenges

COPY --chown=app:app . /app

WORKDIR /app
USER app:app

ENV PORT=5000
RUN npm ci --only=production

# End of share layers for all builds of the same node challenge

ARG FLAG
ARG SEED

USER root:root
RUN install -d -m 0700 /challenge && \
    echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json

RUN find /app \( -name *.js -o -name *.txt -o -name *.html \) \
              -exec sed -i -e "s|{{flag}}|$FLAG|g"            \
                           -e "s|{{seed}}|$SEED|g"            \
                        {} \;

USER app:app
CMD node server.js

EXPOSE 5000
# PUBLISH 5000 AS http
