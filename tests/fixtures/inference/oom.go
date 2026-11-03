// This isolated integration workload requests 256 MiB inside a 64 MiB test cgroup; it performs no model/GPU execution.
package main

import (
	"runtime"
	"time"
)

func main() {
	data := make([]byte, 256*1024*1024)
	for index := 0; index < len(data); index += 4096 {
		data[index] = 1
	}
	time.Sleep(30 * time.Second)
	runtime.KeepAlive(data)
}
