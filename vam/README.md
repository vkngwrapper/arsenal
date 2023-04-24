# vam

A pure-go port of VulkanMemoryAllocator. The ported code is split between this package and arsenal/memutils,
 which contains the portion of VulkanMemoryAllocator which seemed like it could be reused with another
 manual memory management library. In particular, the TLSF implementation, the linear manager implementation,
 and the lion's share of the defragmentation code all live in memutils.

This seems to be implemented (see full_test.go for examples) but there are goals still unmet, zero
 automated tests, and no documentation.

What's left:

* Port vmaBuildStatsString
* Figure out whether we need to implement the name vs. userdata divide used by VMA
* VK_WHOLE_SIZE added to vkngwrapper/core and used here
* Missing extension support added to vkngwrapper/extensions and supported here
* Incorporate a RWLock into allocation.Map/Unmap, and make use of it for the defrag process
* Automated tests
* Documentation