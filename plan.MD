1. Create a new project and setup the cluster there with the same bucket, but different domain.
2. Fill in secrets
3. Prep Billing
2. Set API node IP address for jobs in the new project
3. Allow traffic on ports 5353 and 50001 for public in the old project
4. Start udp forwarding
5. Test if you can create sandbox on the new cluster (you should be forwarded)
6. REMOVE A CLOUDFLARE DNS RECORD FOR ADDITIONAL DOMAINS from terraform and then continue
7. Add additional domain
8. Wait for certificate verification
9. Increase size of the cluster
9. Update A record
10. Wait till all traffic is from the new cluster
11. Send request to stop proxying
12. Roll update API
13. Celebrate