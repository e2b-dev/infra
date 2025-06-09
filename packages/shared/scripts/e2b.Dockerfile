FROM ubuntu:latest

RUN useradd -m user && \
    chown -R user:user /home/user
