package ask

import "net"

func netDial(sock string) (net.Conn, error) { return net.Dial("unix", sock) }
