# ECDS Fault Injection POC

This demonstrates how ECDS can be used to provide fault injection to an Envoy instance.

Start the test with 

```
docker-compose up
```

and observe that the proxied calls fail with a connection error:

```
➜  ~ curl localhost:5000/whatever
upstream connect error or disconnect/reset before headers. reset reason: connection failure%
```

now update the fault % by issuing a call to the ECDS server:

```
curl 'localhost:8000/fault?fault=0'
```

and now you can observe the calls fail due to an abort instead:

```
➜  ~ curl localhost:5000/whatever
fault filter abort%
```
