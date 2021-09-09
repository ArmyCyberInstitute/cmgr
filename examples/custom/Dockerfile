FROM alpine AS base

RUN apk update
RUN apk add openssl socat

# Every challenge must place files into this directory
RUN mkdir /challenge
RUN echo `date` > time.txt

FROM base AS challenge

ARG FLAG

# The "flag" field in this json object is mandatory, the rest are lookup fields.
RUN echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json
RUN echo $FLAG | openssl aes-256-cbc -k unguessable -pbkdf2 -out secret.enc

# These "artifacts" are available to competitors for download
RUN tar czvf /challenge/artifacts.tar.gz secret.enc time.txt

CMD socat TCP4-LISTEN:4242,reuseaddr,fork exec:'echo -n openssl aes-256-cbc -d -k unguessable -pbkdf2 -in secret.enc'

EXPOSE 4200
# PUBLISH 4200 AS socat
