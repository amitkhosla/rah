# Smart-Slab Cache System

The cache uses **Zero-GC Slab Allocation**. Data is stored in large contiguous segments to prevent Go's garbage collector from causing latency spikes on your 2-core CPU.

## SmartPointer Layout (64-bit)
| Bits | Purpose | Description |
| :--- | :--- | :--- |
| 63-62 | **Tag** | 00: Standard Slab, 01: Tiny (Fixed 24B) |
| 61-56 | **SlabID** | Maps to a specific TTL/Size bucket |
| 55-52 | **Version** | Steamroller protection (checks if memory was reused) |
| 51-28 | **Length** | Item size up to 16MB |
| 27-00 | **Offset** | Direct byte address in segment |

## Memory Safety: The Triple Validation
Every `Get` operation performs:
1. **Magic Byte Check**: Ensures the pointer isn't hitting junk data.
2. **Version Check**: Ensures the slab hasn't "wrapped around" and overwritten the data.
3. **TenantID Check**: Prevents "Cache Bleeding" where Tenant A sees Tenant B's data.