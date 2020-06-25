FROM ubuntu:20.04
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update
RUN apt-get -y install python3-pip python3-dev git libssl-dev libffi-dev build-essential
RUN pip3 install pwntools

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then xargs -a packages.txt apt-get install -y; fi

COPY Dockerfile requirements.txt* ./
RUN if [ -f requirements.txt ]; then pip3 install -r requirements.txt; fi

COPY . /solve
WORKDIR /solve

CMD python3 solve.py
