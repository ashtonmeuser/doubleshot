#ifndef DOUBLESHOT_POWER_H
#define DOUBLESHOT_POWER_H
#include <stdint.h>
#include <stddef.h>
int32_t ds_power_open(uint32_t *handle);
int32_t ds_power_set(uint32_t handle, int enabled);
int32_t ds_power_close(uint32_t handle);
int32_t ds_idle_create(uint32_t *assertion);
int32_t ds_idle_release(uint32_t assertion);
const char *ds_power_error(int32_t code);
int ds_user_temp(char *buffer, size_t capacity);
typedef struct {
    int external_display;
    int ac_power;
    int lid_closed;
} ds_status;
int32_t ds_read_status(ds_status *status);
#endif
