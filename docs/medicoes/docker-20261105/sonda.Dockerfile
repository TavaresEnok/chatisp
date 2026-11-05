FROM debian:13-slim
RUN getent hosts deb.debian.org && apt-get update -o Acquire::Retries=0 >/dev/null && echo NET_OK
