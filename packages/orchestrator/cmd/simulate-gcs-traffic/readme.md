To run inside of a host in GCS, trust this fancy one-liner:

    host=<user>@<ip address> && bucket=<bucket> && go build -o simulator . && ssh $host rm ./simulator && scp ./simulator $host:~/simulator && ssh $host -- ./simulator -csv-path gcs.csv -test-duration=5s $bucket && ssh $host cat gcs.csv
