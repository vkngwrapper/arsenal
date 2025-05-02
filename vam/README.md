# vam

[![Go Reference](https://pkg.go.dev/badge/github.com/vkngwrapper/arsenal/vam.svg)](https://pkg.go.dev/github.com/vkngwrapper/arsenal/vam)

A pure-go port of VulkanMemoryAllocator. The ported code is split between this package and arsenal/memutils,
 which contains the portion of VulkanMemoryAllocator which seemed like it could be reused with another
 manual memory management library. In particular, the TLSF implementation, the linear manager implementation,
 and the lion's share of the defragmentation code all live in memutils.

This library can be used to manage buffer and image memory for a Vulkan application. 