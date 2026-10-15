//go:build darwin && arm64 && cgo && !doubleshot_test

#include "power.h"
#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CoreGraphics.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/ps/IOPowerSources.h>
#include <IOKit/ps/IOPSKeys.h>
#include <IOKit/pwr_mgt/IOPMLibDefs.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <mach/mach.h>
#include <stdbool.h>
#include <stdlib.h>
#include <unistd.h>

int32_t ds_power_open(uint32_t *handle) {
    io_service_t service = IOServiceGetMatchingService(
        kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"));
    if (!service) return kIOReturnNotFound;
    kern_return_t result = IOServiceOpen(service, mach_task_self(), 0, handle);
    IOObjectRelease(service);
    return result;
}
int32_t ds_power_set(uint32_t handle, int enabled) {
    uint64_t input = enabled ? 1 : 0;
    uint32_t count = 0;
    return IOConnectCallScalarMethod(handle, kPMSetClamshellSleepState,
        &input, 1, NULL, &count);
}
static int external_display(void) {
    uint32_t count = 0, actual = 0;
    if (CGGetOnlineDisplayList(0, NULL, &count) != kCGErrorSuccess) return -1;
    CGDirectDisplayID *displays = calloc(count ? count : 1, sizeof(*displays));
    if (!displays) return -1;
    if (CGGetOnlineDisplayList(count, displays, &actual) != kCGErrorSuccess) {
        free(displays);
        return -1;
    }
    bool external = false;
    for (uint32_t i = 0; i < actual; i++) external |= !CGDisplayIsBuiltin(displays[i]);
    free(displays);
    return external;
}
static int ac_power(void) {
    CFTypeRef info = IOPSCopyPowerSourcesInfo();
    if (!info) return -1;
    CFStringRef source = IOPSGetProvidingPowerSourceType(info);
    int ac = source ? CFEqual(source, CFSTR(kIOPSACPowerValue)) : -1;
    CFRelease(info);
    return ac;
}
static int read_bool(io_service_t service, CFStringRef key) {
    CFTypeRef value = IORegistryEntryCreateCFProperty(service, key, kCFAllocatorDefault, 0);
    if (!value) return -1;
    int result = CFGetTypeID(value) == CFBooleanGetTypeID()
        ? CFBooleanGetValue((CFBooleanRef)value) : -1;
    CFRelease(value);
    return result;
}
int32_t ds_read_status(ds_status *status) {
    *status = (ds_status){external_display(), ac_power(), -1};
    io_service_t service = IOServiceGetMatchingService(
        kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"));
    if (!service) return kIOReturnNotFound;
    status->lid_closed = read_bool(service, CFSTR("AppleClamshellState"));
    IOObjectRelease(service);
    return kIOReturnSuccess;
}
int32_t ds_power_close(uint32_t handle) { return IOServiceClose(handle); }
int32_t ds_idle_create(uint32_t *assertion) {
    return IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleSystemSleep,
        kIOPMAssertionLevelOn, CFSTR("doubleshot"), assertion);
}
int32_t ds_idle_release(uint32_t assertion) { return IOPMAssertionRelease(assertion); }
const char *ds_power_error(int32_t code) { return mach_error_string(code); }
int ds_user_temp(char *buffer, size_t capacity) {
    size_t n = confstr(_CS_DARWIN_USER_TEMP_DIR, buffer, capacity);
    return n > 1 && n <= capacity ? 0 : -1;
}
