# memutils

[![Go Reference](https://pkg.go.dev/badge/github.com/vkngwrapper/arsenal/memutils.svg)](https://pkg.go.dev/github.com/vkngwrapper/arsenal/memutils)

A pure-go port of some parts of VulkanMemoryAllocator.  The rest is in arsenal/vam, which 
 contains all Vulkan-specific code and consumes this library.

This library contains an implementation of Two-Level Segregated Fit (TLSF), a linear allocator
 (useful for arenas, stacks, and ring buffers), and defragmentation logic.

This library can hypothetically be used with other memory providers than vam, and other memory systems
 than Vulkan.  It may be worthwhile to try to build a system that manages memory in a large raw
 bytes buffer for algorithms and programs that use a great deal of heap allocations.  TLSF is a useful
 algorithm and it is here for any who want to use it.
