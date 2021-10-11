# heey
heey is a wrapper command that proportionally controls the parameters of external command commands.


## Installation

```shell
go install github.com/tetsuzawa/heey@latest
```

## Usage

```
Usage: heey [options...] <reporter_url> "<external_command> <args>..."

Options:

  -kp     Proportional control gain. Default is 1000
  -sv     Set variable. heey performs proportional control so that pv becomes sv.
          sv must be in 0 to 100. Default is 50.
  -mv     Initial manipulative variable. Default is 1000.
  -i      Interval of observation [ms]. Default is 1000
  -l      Buffer Length of observation. The observed value (pv) is the average value of the buffer.
  -macro  Macro is the placeholder to replace the control value (mv). Default is '%'.


  -m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
  -H  Custom HTTP header. You can specify as many as needed by repeating the flag.
      For example, -H "Accept: text/html" -H "Content-Type: application/xml" .
  -t  Timeout for each request in seconds. Default is 20, use 0 for infinite.
  -A  HTTP Accept header.
  -d  HTTP request body.
  -D  HTTP request body from file. For example, /home/user/file.txt or ./file.txt.
  -T  Content-type, defaults to "text/html".
  -U  User-Agent, defaults to version "hey/0.0.1".
  -a  Basic authentication, username:password.
  -x  HTTP Proxy address as host:port.
  -h2 Enable HTTP/2.

  -host	HTTP Host header.

  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                        connections between different HTTP requests.
  -disable-redirects    Disable following of HTTP redirects
```

## Example

```shell
heey -kp 100 -i 1000 -l 5 http://13.115.10.112:6000/cpu "hey -c 100 -n 1000000 -q % http://13.115.10.112:6000/target"
```

