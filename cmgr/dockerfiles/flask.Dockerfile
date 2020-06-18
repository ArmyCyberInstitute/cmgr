FROM ubuntu:20.04

RUN apt-get update -y
RUN apt-get -y install python3-pip
RUN pip3 install flask

COPY . /app

ENV FLASK_RUN_HOST=0.0.0.0
ENV FLASK_RUN_PORT=8000

ARG FLAG
ARG SEED

RUN mkdir /challenge && echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json

RUN sed -i -e "s|{{flag}}|$FLAG|g"                                        \
           -e "s|{{secret_key}}|$(echo $FLAG | md5sum | cut -d' ' -f1)|g" \
           -e "s|{{seed}}|$SEED|g"                                        \
        /app/app.py

WORKDIR /app
CMD flask run

EXPOSE 8000
# PUBLISH 8000 AS http
