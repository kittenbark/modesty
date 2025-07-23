FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1
ENV PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-venv \
    python3-dev \
    build-essential \
    wget \
    curl \
    git \
    libjpeg-dev \
    libpng-dev \
    libtiff-dev \
    libwebp-dev \
    zlib1g-dev \
    libfreetype6-dev \
    liblcms2-dev \
    libopenjp2-7-dev \
    libtk8.6 \
    libffi-dev \
    libssl-dev \
    && rm -rf /var/lib/apt/lists/*

RUN python3 -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"
RUN pip install --upgrade pip setuptools wheel
RUN pip install torch torchvision --index-url https://download.pytorch.org/whl/cpu
RUN pip install transformers pillow requests huggingface-hub tokenizers
RUN mkdir -p /app/models

COPY ./server.py /app/
CMD ["python3", "server.py"]
