package grpcutil

import (
	"fmt"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ServiceInfo describes one gRPC service within a descriptor set.
type ServiceInfo struct {
	FullName string   // fully-qualified service name, e.g. "com.example.UserService"
	Methods  []string // method names (not full paths), e.g. ["GetUser", "ListUsers"]
}

// registryEntry holds the parsed state for one named FileDescriptorSet.
type registryEntry struct {
	raw      []byte                                       // original bytes (for persistence)
	services map[string]protoreflect.ServiceDescriptor   // fullName → descriptor
}

// DescriptorRegistry stores parsed FileDescriptorSets by name and provides
// thread-safe lookup for service methods. Safe for concurrent use.
type DescriptorRegistry struct {
	mu   sync.RWMutex
	sets map[string]*registryEntry
}

// NewDescriptorRegistry returns an initialized, empty registry.
func NewDescriptorRegistry() *DescriptorRegistry {
	return &DescriptorRegistry{sets: make(map[string]*registryEntry)}
}

// Load parses a FileDescriptorSet blob and registers it under the given name.
// Existing entries with the same name are replaced.
// Returns an error if the bytes cannot be parsed or dependencies are missing.
func (r *DescriptorRegistry) Load(name string, data []byte) error {
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, fds); err != nil {
		return fmt.Errorf("grpc descriptor %q: unmarshal: %w", name, err)
	}

	// Build a local protoregistry.Files — never touch the global registry.
	filesReg := new(protoregistry.Files)

	// Topological sort (Kahn's algorithm): register files only after their
	// dependencies are already in filesReg.
	remaining := make([]*descriptorpb.FileDescriptorProto, len(fds.File))
	copy(remaining, fds.File)

	for pass := 0; len(remaining) > 0; pass++ {
		if pass > len(fds.File) {
			// We've done more passes than files — circular dependency.
			names := make([]string, len(remaining))
			for i, f := range remaining {
				names[i] = f.GetName()
			}
			return fmt.Errorf("grpc descriptor %q: circular or missing dependency among: %v", name, names)
		}

		var deferred []*descriptorpb.FileDescriptorProto
		for _, fdp := range remaining {
			// Check all imports are already registered.
			allReady := true
			for _, dep := range fdp.Dependency {
				if _, err := filesReg.FindFileByPath(dep); err != nil {
					allReady = false
					break
				}
			}
			if !allReady {
				deferred = append(deferred, fdp)
				continue
			}

			fd, err := protodesc.NewFile(fdp, filesReg)
			if err != nil {
				return fmt.Errorf("grpc descriptor %q: file %q: %w", name, fdp.GetName(), err)
			}
			if err := filesReg.RegisterFile(fd); err != nil {
				// Duplicate registration is fine (well-known types appear in multiple sets).
				// Any other error is fatal.
				if _, lookupErr := filesReg.FindFileByPath(fdp.GetName()); lookupErr != nil {
					return fmt.Errorf("grpc descriptor %q: register %q: %w", name, fdp.GetName(), err)
				}
			}
		}
		remaining = deferred
	}

	// Collect all ServiceDescriptors from all registered files.
	svcMap := make(map[string]protoreflect.ServiceDescriptor)
	filesReg.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			svc := svcs.Get(i)
			svcMap[string(svc.FullName())] = svc
		}
		return true
	})

	entry := &registryEntry{
		raw:      data,
		services: svcMap,
	}

	r.mu.Lock()
	r.sets[name] = entry
	r.mu.Unlock()
	return nil
}

// FindMethod looks up a method descriptor by descriptor set name, service name, and method name.
func (r *DescriptorRegistry) FindMethod(setName, service, method string) (protoreflect.MethodDescriptor, error) {
	r.mu.RLock()
	entry, ok := r.sets[setName]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("grpc: descriptor set %q not found", setName)
	}
	svc, ok := entry.services[service]
	if !ok {
		return nil, fmt.Errorf("grpc: service %q not found in descriptor set %q", service, setName)
	}
	md := svc.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, fmt.Errorf("grpc: method %q not found in service %q (descriptor set %q)", method, service, setName)
	}
	return md, nil
}

// FindMessage looks up a message descriptor by descriptor set name and fully-qualified message name.
// The message name should be the full path (e.g., "package.MessageName").
func (r *DescriptorRegistry) FindMessage(setName, msgName string) (protoreflect.MessageDescriptor, error) {
	r.mu.RLock()
	entry, ok := r.sets[setName]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("grpc: descriptor set %q not found", setName)
	}

	// Reconstruct a protoregistry from the stored raw descriptor bytes.
	// To properly look up messages, we rebuild the Files registry from fds.
	filesReg := new(protoregistry.Files)

	// Re-deserialize the descriptor set to get access to all types.
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(entry.raw, fds); err != nil {
		return nil, fmt.Errorf("grpc: re-parse descriptor set %q: %w", setName, err)
	}

	// Rebuild filesReg from fds.
	remaining := make([]*descriptorpb.FileDescriptorProto, len(fds.File))
	copy(remaining, fds.File)

	for pass := 0; len(remaining) > 0; pass++ {
		if pass > len(fds.File) {
			return nil, fmt.Errorf("grpc: circular dependency while rebuilding descriptor set %q", setName)
		}

		var deferred []*descriptorpb.FileDescriptorProto
		for _, fdp := range remaining {
			allReady := true
			for _, dep := range fdp.Dependency {
				if _, err := filesReg.FindFileByPath(dep); err != nil {
					allReady = false
					break
				}
			}
			if !allReady {
				deferred = append(deferred, fdp)
				continue
			}

			fd, err := protodesc.NewFile(fdp, filesReg)
			if err != nil {
				return nil, fmt.Errorf("grpc: build file %q: %w", fdp.GetName(), err)
			}
			if err := filesReg.RegisterFile(fd); err != nil {
				if _, lookupErr := filesReg.FindFileByPath(fdp.GetName()); lookupErr != nil {
					return nil, fmt.Errorf("grpc: register file %q: %w", fdp.GetName(), err)
				}
			}
		}
		remaining = deferred
	}

	// Now search for the message by name.
	var found protoreflect.MessageDescriptor
	filesReg.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		msgs := fd.Messages()
		for i := 0; i < msgs.Len(); i++ {
			msg := msgs.Get(i)
			if string(msg.FullName()) == msgName {
				found = msg
				return false
			}
		}
		return true
	})

	if found == nil {
		return nil, fmt.Errorf("grpc: message %q not found in descriptor set %q", msgName, setName)
	}
	return found, nil
}

// ListServices returns metadata about all services in the named descriptor set.
// Returns nil if the set is not found.
func (r *DescriptorRegistry) ListServices(setName string) []ServiceInfo {
	r.mu.RLock()
	entry, ok := r.sets[setName]
	r.mu.RUnlock()

	if !ok {
		return nil
	}

	out := make([]ServiceInfo, 0, len(entry.services))
	for fullName, svc := range entry.services {
		info := ServiceInfo{FullName: fullName}
		methods := svc.Methods()
		for i := 0; i < methods.Len(); i++ {
			info.Methods = append(info.Methods, string(methods.Get(i).Name()))
		}
		sort.Strings(info.Methods)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return out
}

// ListSets returns the names of all loaded descriptor sets, sorted alphabetically.
func (r *DescriptorRegistry) ListSets() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.sets))
	for n := range r.sets {
		names = append(names, n)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

// Delete removes a descriptor set by name. No-op if not found.
func (r *DescriptorRegistry) Delete(name string) {
	r.mu.Lock()
	delete(r.sets, name)
	r.mu.Unlock()
}

// RawBytes returns the original FileDescriptorSet bytes for the named set,
// or nil if not found. Used for persistence.
func (r *DescriptorRegistry) RawBytes(name string) []byte {
	r.mu.RLock()
	entry, ok := r.sets[name]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return entry.raw
}
