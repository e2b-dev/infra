#!/bin/bash

# Variables
LOCAL_FILE="$(pwd)/dns-forwarder"     # Change to your local file path
REMOTE_PATH="/home/jakub"            # Change to the target path on the VM
# Check if there are 3 arguments
if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <prefix> <zone> <project_id>"
    exit 1
fi

PREFIX="$1"
GCP_ZONE="$2"
GCP_PROJECT_ID=$3

VM_NAME=$(gcloud compute instance-groups list-instances "${PREFIX}orch-api-ig" \
                  	  --zone="${GCP_ZONE}" \
                  	  --project="${GCP_PROJECT_ID}" \
                  	  --format="value(instance)")
# Copy file to GCP VM
gcloud compute scp "$LOCAL_FILE" "jakub@$VM_NAME:$REMOTE_PATH" --zone "$GCP_ZONE" --project="${GCP_PROJECT_ID}"

# Check if the copy was successful
if [ $? -eq 0 ]; then
    echo "File copied successfully to $REMOTE_USER@$VM_NAME:$REMOTE_PATH"
else
    echo "Failed to copy file."
fi

# ssh to the VM
gcloud compute ssh "jakub@$VM_NAME" --project="${GCP_PROJECT_ID}" --zone "$GCP_ZONE" --command "sudo chmod +x $REMOTE_PATH/dns-forwarder && nohup $REMOTE_PATH/dns-forwarder > /dev/null 2>&1 &" &
