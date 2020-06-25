FROM ubuntu:20.04
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update
RUN apt-get -y install python3-pip build-essential
RUN pip3 install flask
RUN groupadd -r flask && useradd -r -d /app -g flask flask

ENV FLASK_RUN_HOST=0.0.0.0
ENV FLASK_RUN_PORT=8000

# End of shared layers for all flask challenges

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then xargs -a packages.txt apt-get install -y; fi

COPY Dockerfile requirements.txt* ./
RUN if [ -f requirements.txt ]; then pip3 install -r requirements.txt; fi

COPY --chown=flask:flask . /app

# End of share layers for all builds of the same flask challenge

ARG FLAG
ARG SEED

RUN install -d -m 0700 /challenge && \
    echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json

USER flask:flask

RUN sed -i -e "s|{{flag}}|$FLAG|g"                                           \
           -e "s|{{secret_key}}|$(echo $FLAG | sha256sum | cut -d' ' -f1)|g" \
           -e "s|{{seed}}|$SEED|g"                                           \
        /app/app.py

WORKDIR /app
CMD flask run

EXPOSE 8000
# PUBLISH 8000 AS http
