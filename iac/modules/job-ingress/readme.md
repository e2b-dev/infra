# Dashboard access

1. Find the name of the VM running a traefik allocation.
2. Run `gcloud compute ssh $NAME -- -NL 8900:localhost:8900`
3. Visit http://localhost:8900/dashboard/
