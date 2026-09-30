package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
)

const (
	SchemaVersion  = 1
	maxNamespaces  = 512
	maxScannedPIDs = 4096
)

type NamespaceRef struct {
	ID                string   `json:"id"`
	RepresentativePID int      `json:"representative_pid"`
	Classes           []string `json:"classes"`
	UserIDs           []int    `json:"user_ids,omitempty"`
	Members           int      `json:"members"`
	Accessible        bool     `json:"accessible"`
	Storage           []string `json:"storage_capabilities,omitempty"`
	ErrorCode         string   `json:"error_code,omitempty"`
}

type AndroidUser struct {
	UserID               int  `json:"user_id"`
	Primary              bool `json:"primary"`
	AppNamespaces        int  `json:"app_namespaces"`
	AccessibleNamespaces int  `json:"accessible_namespaces"`
	VisibleNamespaces    int  `json:"visible_namespaces"`
	Qualified            bool `json:"qualified"`
}

type Topology struct {
	SchemaVersion       int            `json:"schema_version"`
	ServicePID          int            `json:"service_pid"`
	ServiceNamespace    string         `json:"service_namespace"`
	Namespaces          []NamespaceRef `json:"namespaces"`
	Users               []int          `json:"users"`
	StorageCapabilities []string       `json:"storage_capabilities,omitempty"`
	ObservedUnixMS      int64          `json:"observed_unix_ms"`
	ScannedPIDs         int            `json:"scanned_pids"`
	NamespaceLimit      int            `json:"namespace_limit"`
	Truncated           bool           `json:"truncated"`
}

type NamespaceVisibility struct {
	NamespaceID       string          `json:"namespace_id"`
	RepresentativePID int             `json:"representative_pid"`
	Classes           []string        `json:"classes"`
	UserIDs           []int           `json:"user_ids,omitempty"`
	Accessible        bool            `json:"accessible"`
	Visible           bool            `json:"visible"`
	Signature         *MountSignature `json:"signature,omitempty"`
	ErrorCode         string          `json:"error_code,omitempty"`
}

type Inspection struct {
	SchemaVersion   int                   `json:"schema_version"`
	Name            string                `json:"name"`
	Mountpoint      string                `json:"mountpoint"`
	ServiceVisible  bool                  `json:"service_visible"`
	SourceOwned     bool                  `json:"source_owned"`
	SourceSignature *MountSignature       `json:"source_signature,omitempty"`
	Visibility      []NamespaceVisibility `json:"visibility"`
	Users           []AndroidUser         `json:"users,omitempty"`
	AchievedClasses []string              `json:"achieved_classes,omitempty"`
	Claim           string                `json:"claim"`
	Topology        Topology              `json:"topology"`
	ObservedUnixMS  int64                 `json:"observed_unix_ms"`
}

type procMember struct {
	PID        int
	UID        int
	UserID     int
	Namespace  string
	Classes    []string
	Infos      []MountInfo
	Accessible bool
	ErrorCode  string
}

type namespaceGroup struct {
	ref   NamespaceRef
	infos []MountInfo
}

func procRoot() string {
	if value := os.Getenv("RNEXUS_PROC_ROOT"); value != "" {
		return value
	}
	return "/proc"
}

func servicePID() int {
	if value := os.Getenv("RNEXUS_SERVICE_PID"); value != "" {
		if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
			return pid
		}
	}
	return os.Getpid()
}

func readUID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		return strconv.Atoi(fields[1])
	}
	return 0, fmt.Errorf("uid unavailable")
}

func readCommand(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if idx := strings.IndexByte(string(data), 0); idx >= 0 {
		data = data[:idx]
	}
	return strings.TrimSpace(string(data))
}

func classify(pid, service, uid int, command string) []string {
	set := map[string]bool{}
	if pid == service {
		set["service"] = true
	}
	if uid == 0 {
		set["root"] = true
	}
	lower := strings.ToLower(command)
	if strings.Contains(lower, "zygote") || strings.Contains(lower, "usap") {
		set["zygote"] = true
	}
	if uid == 2000 {
		set["shell"] = true
	}
	appID := uid % 100000
	if appID >= 10000 {
		set["app"] = true
		if strings.Contains(lower, "com.termux") {
			set["termux"] = true
		}
	}
	if len(set) == 0 {
		set["system"] = true
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func readMember(root string, pid, service int) (procMember, error) {
	base := filepath.Join(root, strconv.Itoa(pid))
	ns, err := os.Readlink(filepath.Join(base, "ns", "mnt"))
	if err != nil {
		return procMember{}, err
	}
	uid, err := readUID(filepath.Join(base, "status"))
	if err != nil {
		return procMember{}, err
	}
	member := procMember{PID: pid, UID: uid, UserID: uid / 100000, Namespace: ns, Classes: classify(pid, service, uid, readCommand(filepath.Join(base, "cmdline")))}
	file, err := os.Open(filepath.Join(base, "mountinfo"))
	if err != nil {
		member.ErrorCode = "mountinfo_unreadable"
		return member, nil
	}
	defer file.Close()
	infos, err := ParseMountInfo(file)
	if err != nil {
		member.ErrorCode = "mountinfo_invalid"
		return member, nil
	}
	member.Accessible, member.Infos = true, infos
	return member, nil
}

func Discover() (Topology, map[string][]MountInfo, error) {
	root, service := procRoot(), servicePID()
	entries, err := os.ReadDir(root)
	if err != nil {
		return Topology{}, nil, err
	}
	groups := map[string]*namespaceGroup{}
	users := map[int]bool{}
	allStorage := map[string]bool{}
	scanned := 0
	truncated := false
	addMember := func(member procMember) {
		group := groups[member.Namespace]
		if group == nil {
			if len(groups) >= maxNamespaces {
				truncated = true
				return
			}
			group = &namespaceGroup{ref: NamespaceRef{ID: member.Namespace, RepresentativePID: member.PID}}
			groups[member.Namespace] = group
		}
		group.ref.Members++
		group.ref.Classes = mergeStrings(group.ref.Classes, member.Classes)
		group.ref.UserIDs = mergeInts(group.ref.UserIDs, []int{member.UserID})
		users[member.UserID] = true
		if member.Accessible && !group.ref.Accessible {
			group.ref.Accessible, group.ref.RepresentativePID, group.infos = true, member.PID, member.Infos
			group.ref.ErrorCode = ""
		} else if !member.Accessible && !group.ref.Accessible && group.ref.ErrorCode == "" {
			group.ref.ErrorCode = member.ErrorCode
		}
	}

	// Always inspect the Nexus service first so output truncation cannot erase
	// the source namespace needed to qualify ownership and visibility.
	if member, memberErr := readMember(root, service, service); memberErr == nil {
		scanned++
		addMember(member)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == service {
			continue
		}
		if scanned >= maxScannedPIDs {
			truncated = true
			break
		}
		scanned++
		member, err := readMember(root, pid, service)
		if err != nil {
			continue
		}
		addMember(member)
	}
	refs := make([]NamespaceRef, 0, len(groups))
	infosByNS := map[string][]MountInfo{}
	serviceNS := ""
	for id, group := range groups {
		group.ref.Storage = storageCapabilities(group.infos)
		for _, capability := range group.ref.Storage {
			allStorage[capability] = true
		}
		refs = append(refs, group.ref)
		infosByNS[id] = group.infos
		if contains(group.ref.Classes, "service") {
			serviceNS = id
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ID == serviceNS {
			return true
		}
		if refs[j].ID == serviceNS {
			return false
		}
		return refs[i].ID < refs[j].ID
	})
	userList := make([]int, 0, len(users))
	for id := range users {
		userList = append(userList, id)
	}
	sort.Ints(userList)
	storage := make([]string, 0, len(allStorage))
	for item := range allStorage {
		storage = append(storage, item)
	}
	sort.Strings(storage)
	return Topology{
		SchemaVersion: SchemaVersion, ServicePID: service, ServiceNamespace: serviceNS,
		Namespaces: refs, Users: userList, StorageCapabilities: storage,
		ObservedUnixMS: time.Now().UnixMilli(), ScannedPIDs: scanned,
		NamespaceLimit: maxNamespaces, Truncated: truncated,
	}, infosByNS, nil
}

func Inspect(p paths.Paths, name string) (Inspection, error) {
	cfg, err := mounts.Parse(p, name)
	if err != nil {
		return Inspection{}, err
	}
	topo, infosByNS, err := Discover()
	if err != nil {
		return Inspection{}, err
	}
	obs, err := mounts.ObserveRuntime(p, name)
	if err != nil {
		return Inspection{}, err
	}
	result := Inspection{SchemaVersion: SchemaVersion, Name: name, Mountpoint: cfg.Mountpoint, SourceOwned: obs.OwnedMount, Topology: topo, ObservedUnixMS: time.Now().UnixMilli()}
	if serviceInfos := infosByNS[topo.ServiceNamespace]; serviceInfos != nil {
		if info, ok := findMount(serviceInfos, cfg.Mountpoint); ok {
			sig := info.Signature()
			result.ServiceVisible = true
			result.SourceSignature = &sig
		}
	}
	users := map[int]*AndroidUser{}
	achieved := map[string]bool{}
	if result.ServiceVisible {
		achieved["service"] = true
	}
	for _, ref := range topo.Namespaces {
		entry := NamespaceVisibility{NamespaceID: ref.ID, RepresentativePID: ref.RepresentativePID, Classes: ref.Classes, UserIDs: ref.UserIDs, Accessible: ref.Accessible, ErrorCode: ref.ErrorCode}
		if ref.Accessible {
			if info, ok := findMount(infosByNS[ref.ID], cfg.Mountpoint); ok {
				entry.Visible = true
				sig := info.Signature()
				entry.Signature = &sig
				for _, class := range ref.Classes {
					achieved[class] = true
				}
			}
		}
		result.Visibility = append(result.Visibility, entry)
		if contains(ref.Classes, "app") {
			for _, userID := range ref.UserIDs {
				u := users[userID]
				if u == nil {
					u = &AndroidUser{UserID: userID, Primary: userID == 0}
					users[userID] = u
				}
				u.AppNamespaces++
				if ref.Accessible {
					u.AccessibleNamespaces++
				}
				if ref.Accessible && entry.Visible {
					u.VisibleNamespaces++
				}
			}
		}
	}
	for _, u := range users {
		u.Qualified = u.AppNamespaces > 0 && u.AccessibleNamespaces == u.AppNamespaces && u.VisibleNamespaces == u.AppNamespaces
		result.Users = append(result.Users, *u)
	}
	sort.Slice(result.Users, func(i, j int) bool { return result.Users[i].UserID < result.Users[j].UserID })
	for class := range achieved {
		result.AchievedClasses = append(result.AchievedClasses, class)
	}
	sort.Strings(result.AchievedClasses)
	result.Claim = visibilityClaim(result)
	return result, nil
}

func visibilityClaim(in Inspection) string {
	if !in.ServiceVisible {
		return "source_not_visible"
	}
	if in.Topology.Truncated {
		return "partial_discovery"
	}
	appCount, qualified := 0, 0
	for _, u := range in.Users {
		appCount += u.AppNamespaces
		if u.Qualified {
			qualified++
		}
	}
	if appCount == 0 {
		return "service_visible_no_app_evidence"
	}
	if qualified == len(in.Users) {
		return "observed_all_discovered_app_namespaces"
	}
	any := false
	for _, u := range in.Users {
		if u.VisibleNamespaces > 0 {
			any = true
		}
	}
	if any {
		return "partial_app_visibility"
	}
	return "service_only"
}

func storageCapabilities(infos []MountInfo) []string {
	set := map[string]bool{}
	for _, item := range infos {
		p := item.Mountpoint
		switch {
		case strings.HasPrefix(p, "/storage/emulated/"):
			set["storage-emulated"] = true
		case strings.HasPrefix(p, "/mnt/runtime/"):
			set["runtime-emulated"] = true
		case strings.HasPrefix(p, "/mnt/pass_through/"):
			set["pass-through-emulated"] = true
		case strings.HasPrefix(p, "/mnt/user/"):
			set["user-primary"] = true
		}
		for _, optional := range item.Optional {
			if strings.HasPrefix(optional, "shared:") {
				set["shared-propagation"] = true
			}
			if strings.HasPrefix(optional, "master:") {
				set["slave-propagation"] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func mergeStrings(a, b []string) []string {
	set := map[string]bool{}
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		set[v] = true
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func mergeInts(a, b []int) []int {
	set := map[int]bool{}
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		set[v] = true
	}
	out := make([]int, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}
func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
