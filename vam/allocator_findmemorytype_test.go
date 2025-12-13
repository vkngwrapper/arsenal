package vam

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/mocks"
	"github.com/vkngwrapper/extensions/v2/khr_maintenance4"
	mock_maintenance4 "github.com/vkngwrapper/extensions/v2/khr_maintenance4/mocks"
	"go.uber.org/mock/gomock"
)

var memoryTypeIndexForImageInfoTestCases = map[string]struct {
	ImageCreateInfo core1_0.ImageCreateInfo
	Alloc           AllocationCreateInfo
	DriverType      core1_0.PhysicalDeviceType
	Maint4Extension bool

	Result        common.VkResult
	ExpectedIndex int
}{
	"TestDeviceLocal": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateCanAlias,
			Usage: MemoryUsageAutoPreferDevice,
		},
		DriverType: core1_0.PhysicalDeviceTypeIntegratedGPU,
		ImageCreateInfo: core1_0.ImageCreateInfo{
			Usage:       core1_0.ImageUsageStorage,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 1,
	},
	"TestDeviceLocalMaint4": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateCanAlias,
			Usage: MemoryUsageAutoPreferDevice,
		},
		DriverType: core1_0.PhysicalDeviceTypeIntegratedGPU,
		ImageCreateInfo: core1_0.ImageCreateInfo{
			Usage:       core1_0.ImageUsageStorage,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Maint4Extension: true,
		Result:          core1_0.VKSuccess,
		ExpectedIndex:   1,
	},
}

func TestFindMemoryTypeIndexForImageInfo(t *testing.T) {
	for testName, testCase := range memoryTypeIndexForImageInfoTestCases {
		t.Run(testName, func(t *testing.T) {
			setup := AllocatorSetup{
				DeviceVersion:      common.Vulkan1_0,
				InstanceExtensions: []string{},
				DeviceExtensions:   []string{},
				MemoryTypes: []core1_0.MemoryType{
					{
						PropertyFlags: 0,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
						HeapIndex:     0,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent | core1_0.MemoryPropertyHostCached,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
						HeapIndex:     2,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCached,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyLazilyAllocated,
						HeapIndex:     0,
					},
				},
				MemoryHeaps: []core1_0.MemoryHeap{
					{
						Size:  8000000000, // 8 GB
						Flags: core1_0.MemoryHeapDeviceLocal,
					},
					{
						Size:  16000000000, // 16 GB
						Flags: 0,
					},
					{
						Size:  200000000, // 200MB
						Flags: core1_0.MemoryHeapDeviceLocal,
					},
				},
				DeviceProperties: core1_0.PhysicalDeviceProperties{
					Limits: &core1_0.PhysicalDeviceLimits{
						BufferImageGranularity: 1,
						NonCoherentAtomSize:    1,
					},
				},
				AllocatorOptions: CreateOptions{},
			}

			ctrl := gomock.NewController(t)

			setup.DeviceProperties.DriverType = testCase.DriverType
			if testCase.Maint4Extension {
				setup.DeviceExtensions = []string{khr_maintenance4.ExtensionName}
			}

			_, _, device, allocator := readyAllocator(t, ctrl, setup)
			if allocator.extensionData.Maintenance4 != nil {
				maint4 := mock_maintenance4.NewMockExtension(ctrl)
				allocator.extensionData.Maintenance4 = maint4
				maint4.EXPECT().DeviceImageMemoryRequirements(device, khr_maintenance4.DeviceImageMemoryRequirements{
					CreateInfo: testCase.ImageCreateInfo,
				}, gomock.Any()).DoAndReturn(func(device core1_0.Device, options khr_maintenance4.DeviceImageMemoryRequirements, outData *core1_1.MemoryRequirements2) error {
					outData.MemoryRequirements = core1_0.MemoryRequirements{
						Size:           1000,
						Alignment:      1,
						MemoryTypeBits: 0xffffffff,
					}
					return nil
				})
			} else {
				image := mocks.EasyMockImage(ctrl)
				image.EXPECT().MemoryRequirements().Return(&core1_0.MemoryRequirements{
					Size:           1000,
					Alignment:      1,
					MemoryTypeBits: 0xffffffff,
				})
				image.EXPECT().Destroy(gomock.Any())

				device.EXPECT().CreateImage(gomock.Any(), testCase.ImageCreateInfo).Return(image, core1_0.VKSuccess, nil)
			}

			index, res, _ := allocator.FindMemoryTypeIndexForImageInfo(testCase.ImageCreateInfo, testCase.Alloc)
			require.Equal(t, testCase.Result, res)

			if res == core1_0.VKSuccess {
				require.Equal(t, testCase.ExpectedIndex, index)
			}
		})
	}
}

var memoryTypeIndexForBufferInfoTestCases = map[string]struct {
	BufferCreateInfo core1_0.BufferCreateInfo
	Alloc            AllocationCreateInfo
	DriverType       core1_0.PhysicalDeviceType
	Maint4Extension  bool

	Result        common.VkResult
	ExpectedIndex int
}{
	"TestDeviceLocal": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateCanAlias,
			Usage: MemoryUsageAutoPreferDevice,
		},
		DriverType: core1_0.PhysicalDeviceTypeIntegratedGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 1,
	},
	"TestDeviceLocalMaint4": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateCanAlias,
			Usage: MemoryUsageAutoPreferDevice,
		},
		DriverType: core1_0.PhysicalDeviceTypeIntegratedGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Maint4Extension: true,
		Result:          core1_0.VKSuccess,
		ExpectedIndex:   1,
	},
	"TestDeviceLocalHostCachedRandomAccess": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateHostAccessRandom | AllocationCreateHostAccessAllowTransferInstead,
			Usage: MemoryUsageAutoPreferDevice,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 5,
	},
	"TestDeviceLocalNotCachedSequentialWrite": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateHostAccessSequentialWrite | AllocationCreateHostAccessAllowTransferInstead,
			Usage: MemoryUsageAutoPreferDevice,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 4,
	},
	"TestDeviceLazyAllocated": {
		Alloc: AllocationCreateInfo{
			Flags: 0,
			Usage: MemoryUsageGPULazilyAllocated,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 6,
	},
	"TestDeviceRandomAccess": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateHostAccessRandom,
			Usage: MemoryUsageAuto,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 3,
	},
	"TestDevicePreferHost": {
		Alloc: AllocationCreateInfo{
			Flags: 0,
			Usage: MemoryUsageAutoPreferHost,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 0,
	},
	"TestSequentialWriteDeviceUsagePreferHost": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateHostAccessSequentialWrite,
			Usage: MemoryUsageAutoPreferHost,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageStorageBuffer,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 2,
	},
	"TestSequentialWriteNoDeviceAccessPreferDevice": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateHostAccessSequentialWrite,
			Usage: MemoryUsageAutoPreferDevice,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageTransferDst,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 4,
	},
	"TestSequentialWriteNoDeviceAccess": {
		Alloc: AllocationCreateInfo{
			Flags: AllocationCreateHostAccessSequentialWrite,
			Usage: MemoryUsageAuto,
		},
		DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
		BufferCreateInfo: core1_0.BufferCreateInfo{
			Size:        1000,
			Usage:       core1_0.BufferUsageTransferDst,
			SharingMode: core1_0.SharingModeExclusive,
		},
		Result:        core1_0.VKSuccess,
		ExpectedIndex: 2,
	},
}

func TestFindMemoryTypeIndexForBufferInfo(t *testing.T) {
	for testName, testCase := range memoryTypeIndexForBufferInfoTestCases {
		t.Run(testName, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			setup := AllocatorSetup{
				DeviceVersion:      common.Vulkan1_0,
				InstanceExtensions: []string{},
				DeviceExtensions:   []string{},
				MemoryTypes: []core1_0.MemoryType{
					{
						PropertyFlags: 0,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
						HeapIndex:     0,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent | core1_0.MemoryPropertyHostCached,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
						HeapIndex:     2,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCached,
						HeapIndex:     1,
					},
					{
						PropertyFlags: core1_0.MemoryPropertyLazilyAllocated,
						HeapIndex:     0,
					},
				},
				MemoryHeaps: []core1_0.MemoryHeap{
					{
						Size:  8000000000, // 8 GB
						Flags: core1_0.MemoryHeapDeviceLocal,
					},
					{
						Size:  16000000000, // 16 GB
						Flags: 0,
					},
					{
						Size:  200000000, // 200MB
						Flags: core1_0.MemoryHeapDeviceLocal,
					},
				},
				DeviceProperties: core1_0.PhysicalDeviceProperties{
					Limits: &core1_0.PhysicalDeviceLimits{
						BufferImageGranularity: 1,
						NonCoherentAtomSize:    1,
					},
				},
				AllocatorOptions: CreateOptions{},
			}

			setup.DeviceProperties.DriverType = testCase.DriverType
			if testCase.Maint4Extension {
				setup.DeviceExtensions = []string{khr_maintenance4.ExtensionName}
			}

			_, _, device, allocator := readyAllocator(t, ctrl, setup)
			if allocator.extensionData.Maintenance4 != nil {
				maint4 := mock_maintenance4.NewMockExtension(ctrl)
				allocator.extensionData.Maintenance4 = maint4
				maint4.EXPECT().DeviceBufferMemoryRequirements(device, khr_maintenance4.DeviceBufferMemoryRequirements{
					CreateInfo: testCase.BufferCreateInfo,
				}, gomock.Any()).DoAndReturn(func(device core1_0.Device, options khr_maintenance4.DeviceBufferMemoryRequirements, outData *core1_1.MemoryRequirements2) error {
					outData.MemoryRequirements = core1_0.MemoryRequirements{
						Size:           1000,
						Alignment:      1,
						MemoryTypeBits: 0xffffffff,
					}
					return nil
				})
			} else {
				buffer := mocks.EasyMockBuffer(ctrl)
				buffer.EXPECT().MemoryRequirements().Return(&core1_0.MemoryRequirements{
					Size:           1000,
					Alignment:      1,
					MemoryTypeBits: 0xffffffff,
				})
				buffer.EXPECT().Destroy(gomock.Any())

				device.EXPECT().CreateBuffer(gomock.Any(), testCase.BufferCreateInfo).Return(buffer, core1_0.VKSuccess, nil)
			}

			index, res, _ := allocator.FindMemoryTypeIndexForBufferInfo(testCase.BufferCreateInfo, testCase.Alloc)
			require.Equal(t, testCase.Result, res)

			if res == core1_0.VKSuccess {
				require.Equal(t, testCase.ExpectedIndex, index)
			}
		})
	}
}
