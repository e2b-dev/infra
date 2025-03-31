CREATE TABLE my_s3_table 
   ON CLUSTER cluster
   (
       `id` UInt64,
       `column1` String
   )
   ENGINE = ReplicatedMergeTree('/clickhouse/{installation}/{cluster}/tables/{shard}/{database}/{table}', '{replica}') 
   ORDER BY id
   SETTINGS storage_policy = 's3';
