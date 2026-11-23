#ifndef NOOK_BLE_DARWIN_H
#define NOOK_BLE_DARWIN_H

// The macOS Bluetooth driver (ble_darwin.m), called from ble_darwin.go.
int nook_ble_start(int maxOut);
int nook_ble_send(long long link, const void *buf, int n);
void nook_ble_stop(void);
int nook_respawn_disclaimed(const char *path, char **argv);
int nook_ble_auth(void);

#endif
