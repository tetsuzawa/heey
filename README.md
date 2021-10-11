# heey
heey is a wrapper command that proportionally controls the parameters of external command commands.


## Installation

```shell
go install github.com/tetsuzawa/heey@latest
```

## Example

```shell
heey -kp 100 -i 1000 -l 5 http://13.115.10.112:6000/cpu "hey -c 100 -n 1000000 -q % http://13.115.10.112:6000/target"
```
