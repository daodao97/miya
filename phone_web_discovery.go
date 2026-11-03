package main

import (
	"context"
	"errors"
	"fmt"
	rt "miya/internal/runtime"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const projectWebServicesPath = ".miya-web-services"

type phoneWebService struct {
	RuntimeToken string     `json:"runtimeToken,omitempty"`
	Owner        *ThreadRef `json:"owner,omitempty"`
	Port         int        `json:"port"`
	Process      string     `json:"process"`
	Host         string     `json:"host,omitempty"`
}

type webListenerProcess struct {
	pid       int
	name, cwd string
	ports     []int
	hosts     map[int]string
}

func discoverProjectWebServices(ctx context.Context, project string) ([]phoneWebService, error) {
	root, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, err
	}
	listeners, err := runWebLSOF(ctx, "-nP", "-a", "-u", strconv.Itoa(os.Getuid()), "-iTCP", "-sTCP:LISTEN", "-F0pcfnt")
	if err != nil {
		return nil, err
	}
	processes := parseWebListeners(listeners)
	if len(processes) == 0 {
		return []phoneWebService{}, nil
	}
	pids := make([]string, 0, len(processes))
	for _, process := range processes {
		pids = append(pids, strconv.Itoa(process.pid))
	}
	cwd, err := runWebLSOF(ctx, "-a", "-p", strings.Join(pids, ","), "-d", "cwd", "-F0pn")
	if err != nil {
		return nil, err
	}
	directories := parseWebProcessDirectories(cwd)
	for i := range processes {
		processes[i].cwd = directories[processes[i].pid]
	}
	candidates := projectWebCandidates(root, processes)
	return probeProjectWebServices(ctx, candidates)
}

func runWebLSOF(ctx context.Context, args ...string) ([]byte, error) {
	binary, err := exec.LookPath("lsof")
	if err != nil {
		return nil, errors.New("电脑端无法查找监听端口，请手动输入端口")
	}
	output, err := exec.CommandContext(ctx, binary, args...).Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// lsof exits with 1 when there are no matching processes/files.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return output, nil
	}
	if err != nil {
		return nil, fmt.Errorf("无法读取电脑端监听进程：%w", err)
	}
	return output, nil
}

func webLSOFFields(data []byte) []string {
	fields := strings.Split(string(data), "\x00")
	for i := range fields {
		fields[i] = strings.TrimLeft(fields[i], "\n")
	}
	return fields
}

func parseWebListeners(data []byte) []webListenerProcess {
	var processes []webListenerProcess
	family := ""
	for _, field := range webLSOFFields(data) {
		if len(field) < 2 {
			continue
		}
		switch field[0] {
		case 'f':
			family = ""
		case 't':
			family = field[1:]
		case 'p':
			pid, err := strconv.Atoi(field[1:])
			if err == nil && pid > 0 {
				processes = append(processes, webListenerProcess{pid: pid})
			}
		case 'c':
			if len(processes) > 0 {
				processes[len(processes)-1].name = field[1:]
			}
		case 'n':
			if len(processes) == 0 {
				continue
			}
			host, value, err := net.SplitHostPort(field[1:])
			if err != nil || host != "*" && !loopbackListenerHost(host) {
				continue
			}
			port, err := parseWebPort(value)
			if err == nil {
				p := &processes[len(processes)-1]
				p.ports = append(p.ports, port)
				address := "127.0.0.1"
				if host == "::1" || host == "::" || host == "*" && family == "IPv6" {
					address = "::1"
				}
				if p.hosts == nil {
					p.hosts = map[int]string{}
				}
				if p.hosts[port] != "127.0.0.1" {
					p.hosts[port] = address
				}
			}
		}
	}
	return processes
}

func loopbackListenerHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (host == "127.0.0.1" || host == "::1" || ip.IsUnspecified())
}

func parseWebProcessDirectories(data []byte) map[int]string {
	out := map[int]string{}
	pid := 0
	for _, field := range webLSOFFields(data) {
		if len(field) < 2 {
			continue
		}
		switch field[0] {
		case 'p':
			pid, _ = strconv.Atoi(field[1:])
		case 'n':
			if pid > 0 {
				out[pid] = field[1:]
			}
		}
	}
	return out
}

func projectWebCandidates(root string, processes []webListenerProcess) []phoneWebService {
	byPort := map[int]phoneWebService{}
	for _, process := range processes {
		if process.cwd == "" {
			continue
		}
		cwd, err := filepath.EvalSymlinks(process.cwd)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		for _, port := range process.ports {
			if _, exists := byPort[port]; !exists {
				byPort[port] = phoneWebService{Port: port, Process: process.name, Host: process.hosts[port]}
			}
		}
	}
	services := make([]phoneWebService, 0, len(byPort))
	for _, service := range byPort {
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Port < services[j].Port })
	return services
}

func probeProjectWebServices(ctx context.Context, candidates []phoneWebService) ([]phoneWebService, error) {
	endpoints := make([]rt.Endpoint, len(candidates))
	for i, service := range candidates {
		endpoints[i] = rt.Endpoint{Host: service.Host, Port: service.Port}
	}
	verified, err := rt.ProbeHTTP(ctx, endpoints)
	if err != nil {
		return nil, err
	}
	services := make([]phoneWebService, 0, len(candidates))
	for i, endpoint := range verified {
		if endpoint.Protocol == "http" {
			service := candidates[i]
			service.Host = endpoint.Host
			services = append(services, service)
		}
	}
	return services, nil
}
