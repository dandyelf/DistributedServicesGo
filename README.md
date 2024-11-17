# DistributedServicesGo
Distributed Scalable Services with Go

Ex00 - 01 key-hash-based balancing so client could explicitly calculate for every entry the list of nodes where it should be stored according to a replication factor.

Ex02  introduce concepts of a Leader and a Follower nodes. Client ONLY interacts with a Leader node. The hashing function to determine where to write replicas is now on Leader, not in client.
