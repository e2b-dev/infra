# Make sure to use this base image
FROM e2bdev/code-interpreter:latest 

# Install some Python packages
RUN pip install cowsay 
