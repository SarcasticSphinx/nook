//go:build darwin && cgo && !nobluetooth

// Bluetooth for macOS, on Apple's CoreBluetooth. The laptop is both:
//  - a peripheral: it advertises the nook service; laptops that connect
//    write to its RX characteristic and get notifications on TX;
//  - a central: it finds other nooks and connects to a few of them.
// Every callback runs on one private queue. Go is called only to hand over
// events, never to wait, so nothing here can deadlock with Go.

#import <CoreBluetooth/CoreBluetooth.h>
#import <Foundation/Foundation.h>
#include "_cgo_export.h"
#include "ble_darwin.h"

static NSString *const kService = @"6E6F6F6B-4C41-4E00-9A3F-2B5C7D1E8F01";
static NSString *const kRX = @"6E6F6F6B-4C41-4E00-9A3F-2B5C7D1E8F02";
static NSString *const kTX = @"6E6F6F6B-4C41-4E00-9A3F-2B5C7D1E8F03";

@interface NookLink : NSObject
@property(nonatomic) long long lid;
@property(nonatomic, strong) CBPeripheral *peripheral; // we connected to it
@property(nonatomic, strong) CBCharacteristic *rx;     // where we write to it
@property(nonatomic, strong) CBCentral *central;       // it connected to us
@property(nonatomic) BOOL up;
@property(nonatomic) CFAbsoluteTime started;
@end

@implementation NookLink
@end

@interface NookBLE : NSObject <CBCentralManagerDelegate, CBPeripheralDelegate, CBPeripheralManagerDelegate>
@property(nonatomic, readonly) dispatch_queue_t queue;
- (instancetype)initWithMaxOut:(int)maxOut;
- (int)trySend:(long long)lid data:(NSData *)d central:(BOOL *)isCentral;
- (void)waitReady:(BOOL)central;
- (void)stop;
@end

@implementation NookBLE {
  dispatch_queue_t _queue;
  CBCentralManager *_cm;
  CBPeripheralManager *_pm;
  CBUUID *_svc, *_rxU, *_txU;
  CBMutableCharacteristic *_rx, *_tx;
  BOOL _added, _stopped;
  int _maxOut;
  NSMutableDictionary<NSUUID *, CBPeripheral *> *_cands; // nooks we have heard
  NSMutableDictionary<NSUUID *, NSNumber *> *_heard, *_failed;
  NSMutableDictionary<NSUUID *, NookLink *> *_out; // links we opened
  NSMutableDictionary<NSUUID *, NookLink *> *_in;  // links others opened
  NSMutableDictionary<NSNumber *, NookLink *> *_byID;
  long long _next;
  dispatch_source_t _timer;
  dispatch_semaphore_t _canWrite, _canNotify;
}

- (dispatch_queue_t)queue {
  return _queue;
}

- (instancetype)initWithMaxOut:(int)maxOut {
  if (!(self = [super init])) return nil;
  _maxOut = maxOut;
  _queue = dispatch_queue_create("nook.bluetooth", DISPATCH_QUEUE_SERIAL);
  _svc = [CBUUID UUIDWithString:kService];
  _rxU = [CBUUID UUIDWithString:kRX];
  _txU = [CBUUID UUIDWithString:kTX];
  _cands = [NSMutableDictionary new];
  _heard = [NSMutableDictionary new];
  _failed = [NSMutableDictionary new];
  _out = [NSMutableDictionary new];
  _in = [NSMutableDictionary new];
  _byID = [NSMutableDictionary new];
  _canWrite = dispatch_semaphore_create(0);
  _canNotify = dispatch_semaphore_create(0);
  _cm = [[CBCentralManager alloc] initWithDelegate:self queue:_queue options:@{CBCentralManagerOptionShowPowerAlertKey : @NO}];
  _pm = [[CBPeripheralManager alloc] initWithDelegate:self queue:_queue options:@{CBPeripheralManagerOptionShowPowerAlertKey : @NO}];
  _timer = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, 0, _queue);
  dispatch_source_set_timer(_timer, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC), 2 * NSEC_PER_SEC, NSEC_PER_SEC / 4);
  __weak NookBLE *weak = self;
  dispatch_source_set_event_handler(_timer, ^{
    [weak tick];
  });
  dispatch_resume(_timer);
  return self;
}

// ---- links ----

- (void)linkUp:(NookLink *)l mtu:(int)mtu {
  l.up = YES;
  l.lid = ++_next;
  _byID[@(l.lid)] = l;
  nookBLELinkUp(l.lid, mtu, 0);
}

- (void)linkDown:(NookLink *)l {
  if (!l.up) return;
  l.up = NO;
  [_byID removeObjectForKey:@(l.lid)];
  nookBLELinkDown(l.lid);
}

- (void)dropOut:(CBPeripheral *)p failed:(BOOL)failed {
  NookLink *l = _out[p.identifier];
  if (!l) return;
  [_out removeObjectForKey:p.identifier];
  [self linkDown:l];
  if (failed) _failed[p.identifier] = @(CFAbsoluteTimeGetCurrent());
}

- (void)giveUp:(CBPeripheral *)p {
  [self dropOut:p failed:YES];
  [_cm cancelPeripheralConnection:p];
}

- (void)dropAllOut {
  for (NookLink *l in [_out allValues]) [self linkDown:l];
  [_out removeAllObjects];
}

- (void)dropAllIn {
  for (NookLink *l in [_in allValues]) [self linkDown:l];
  [_in removeAllObjects];
}

// fill opens links to a few nooks we have heard recently, picked at random so
// thirty laptops end up spread out instead of all piling onto one.
- (void)fill {
  if (_stopped || _cm.state != CBManagerStatePoweredOn) return;
  CFAbsoluteTime now = CFAbsoluteTimeGetCurrent();
  NSMutableArray<NSUUID *> *pool = [NSMutableArray new];
  for (NSUUID *u in _cands) {
    NSNumber *f = _failed[u], *h = _heard[u];
    if (_out[u] || (f && now - f.doubleValue < 8) || !h || now - h.doubleValue > 20) continue;
    [pool addObject:u];
  }
  while (_out.count < (NSUInteger)_maxOut && pool.count > 0) {
    NSUInteger i = arc4random_uniform((uint32_t)pool.count);
    NSUUID *u = pool[i];
    [pool removeObjectAtIndex:i];
    NookLink *l = [NookLink new];
    l.peripheral = _cands[u];
    l.started = now;
    _out[u] = l;
    [_cm connectPeripheral:l.peripheral options:nil];
  }
}

- (void)tick {
  if (_stopped) return;
  CFAbsoluteTime now = CFAbsoluteTimeGetCurrent();
  for (NSUUID *u in [_out allKeys]) {
    NookLink *l = _out[u];
    if (!l.up && now - l.started > 12) [self giveUp:l.peripheral];
  }
  for (NSUUID *u in [_heard allKeys]) {
    if (now - _heard[u].doubleValue > 60 && !_out[u]) {
      [_heard removeObjectForKey:u];
      [_cands removeObjectForKey:u];
    }
  }
  [self fill];
}

// ---- central: finding and connecting to other nooks ----

- (void)centralManagerDidUpdateState:(CBCentralManager *)central {
  nookBLEState(0, (int)central.state);
  if (central.state == CBManagerStatePoweredOn && !_stopped) {
    [_cm scanForPeripheralsWithServices:@[ _svc ] options:@{CBCentralManagerScanOptionAllowDuplicatesKey : @YES}];
  } else {
    [self dropAllOut];
  }
}

- (void)centralManager:(CBCentralManager *)central
    didDiscoverPeripheral:(CBPeripheral *)p
        advertisementData:(NSDictionary<NSString *, id> *)ad
                     RSSI:(NSNumber *)rssi {
  if (_stopped) return;
  _cands[p.identifier] = p;
  _heard[p.identifier] = @(CFAbsoluteTimeGetCurrent());
  if (_out.count < (NSUInteger)_maxOut && !_out[p.identifier]) [self fill];
}

- (void)centralManager:(CBCentralManager *)central didConnectPeripheral:(CBPeripheral *)p {
  if (!_out[p.identifier]) {
    [_cm cancelPeripheralConnection:p];
    return;
  }
  p.delegate = self;
  [p discoverServices:@[ _svc ]];
}

- (void)centralManager:(CBCentralManager *)central didFailToConnectPeripheral:(CBPeripheral *)p error:(NSError *)e {
  [self dropOut:p failed:YES];
}

- (void)centralManager:(CBCentralManager *)central didDisconnectPeripheral:(CBPeripheral *)p error:(NSError *)e {
  [self dropOut:p failed:e != nil];
}

- (void)peripheral:(CBPeripheral *)p didDiscoverServices:(NSError *)e {
  CBService *svc = nil;
  for (CBService *s in p.services)
    if ([s.UUID isEqual:_svc]) svc = s;
  if (e || !svc) {
    [self giveUp:p];
    return;
  }
  [p discoverCharacteristics:@[ _rxU, _txU ] forService:svc];
}

- (void)peripheral:(CBPeripheral *)p didDiscoverCharacteristicsForService:(CBService *)s error:(NSError *)e {
  CBCharacteristic *rx = nil, *tx = nil;
  for (CBCharacteristic *c in s.characteristics) {
    if ([c.UUID isEqual:_rxU]) rx = c;
    if ([c.UUID isEqual:_txU]) tx = c;
  }
  NookLink *l = _out[p.identifier];
  if (e || !rx || !tx || !l) {
    [self giveUp:p];
    return;
  }
  l.rx = rx;
  [p setNotifyValue:YES forCharacteristic:tx];
}

- (void)peripheral:(CBPeripheral *)p didUpdateNotificationStateForCharacteristic:(CBCharacteristic *)c error:(NSError *)e {
  NookLink *l = _out[p.identifier];
  if (!l || ![c.UUID isEqual:_txU]) return;
  if (e || !c.isNotifying) {
    [self giveUp:p];
    return;
  }
  if (!l.up) [self linkUp:l mtu:(int)[p maximumWriteValueLengthForType:CBCharacteristicWriteWithoutResponse]];
}

- (void)peripheral:(CBPeripheral *)p didUpdateValueForCharacteristic:(CBCharacteristic *)c error:(NSError *)e {
  NookLink *l = _out[p.identifier];
  NSData *d = c.value;
  if (e || !l || !l.up || ![c.UUID isEqual:_txU] || d.length == 0) return;
  nookBLERecv(l.lid, (void *)d.bytes, (int)d.length);
}

- (void)peripheralIsReadyToSendWriteWithoutResponse:(CBPeripheral *)p {
  dispatch_semaphore_signal(_canWrite);
}

// ---- peripheral: being found, and accepting links ----

- (void)peripheralManagerDidUpdateState:(CBPeripheralManager *)pm {
  nookBLEState(1, (int)pm.state);
  if (pm.state == CBManagerStatePoweredOn) {
    if (!_added && !_stopped) [self addService];
  } else {
    _added = NO;
    [self dropAllIn];
  }
}

- (void)addService {
  _rx = [[CBMutableCharacteristic alloc] initWithType:_rxU
                                           properties:CBCharacteristicPropertyWriteWithoutResponse
                                                value:nil
                                          permissions:CBAttributePermissionsWriteable];
  _tx = [[CBMutableCharacteristic alloc] initWithType:_txU
                                           properties:CBCharacteristicPropertyNotify
                                                value:nil
                                          permissions:CBAttributePermissionsReadable];
  CBMutableService *s = [[CBMutableService alloc] initWithType:_svc primary:YES];
  s.characteristics = @[ _rx, _tx ];
  [_pm removeAllServices];
  [_pm addService:s];
  _added = YES;
}

- (void)peripheralManager:(CBPeripheralManager *)pm didAddService:(CBService *)s error:(NSError *)e {
  if (e) {
    _added = NO;
    nookBLEState(2, 0);
    return;
  }
  // No name in the advertisement: only the service id, which means nothing
  // to anyone looking at a Bluetooth scanner.
  [_pm startAdvertising:@{CBAdvertisementDataServiceUUIDsKey : @[ _svc ]}];
}

- (void)peripheralManagerDidStartAdvertising:(CBPeripheralManager *)pm error:(NSError *)e {
  nookBLEState(2, e ? 0 : 1);
}

- (void)peripheralManager:(CBPeripheralManager *)pm central:(CBCentral *)c didSubscribeToCharacteristic:(CBCharacteristic *)ch {
  if (![ch.UUID isEqual:_txU]) return;
  NookLink *l = _in[c.identifier];
  if (l && l.up) return;
  if (!l) {
    l = [NookLink new];
    l.central = c;
    _in[c.identifier] = l;
  }
  [self linkUp:l mtu:(int)c.maximumUpdateValueLength];
}

- (void)peripheralManager:(CBPeripheralManager *)pm central:(CBCentral *)c didUnsubscribeFromCharacteristic:(CBCharacteristic *)ch {
  NookLink *l = _in[c.identifier];
  if (!l) return;
  [_in removeObjectForKey:c.identifier];
  [self linkDown:l];
}

- (void)peripheralManager:(CBPeripheralManager *)pm didReceiveWriteRequests:(NSArray<CBATTRequest *> *)reqs {
  for (CBATTRequest *r in reqs) {
    NookLink *l = _in[r.central.identifier];
    if (!l || !l.up || ![r.characteristic.UUID isEqual:_rxU] || r.value.length == 0) continue;
    nookBLERecv(l.lid, (void *)r.value.bytes, (int)r.value.length);
  }
}

- (void)peripheralManagerIsReadyToUpdateSubscribers:(CBPeripheralManager *)pm {
  dispatch_semaphore_signal(_canNotify);
}

// ---- sending (runs on _queue, called from nook_ble_send) ----

// trySend returns 0 when sent, 1 when Bluetooth is busy, -1 when the link is gone.
- (int)trySend:(long long)lid data:(NSData *)d central:(BOOL *)isCentral {
  NookLink *l = _byID[@(lid)];
  if (_stopped || !l || !l.up) return -1;
  if (l.peripheral) {
    *isCentral = YES;
    if (l.peripheral.state != CBPeripheralStateConnected || !l.rx) return -1;
    if (!l.peripheral.canSendWriteWithoutResponse) return 1;
    [l.peripheral writeValue:d forCharacteristic:l.rx type:CBCharacteristicWriteWithoutResponse];
    return 0;
  }
  *isCentral = NO;
  return [_pm updateValue:d forCharacteristic:_tx onSubscribedCentrals:@[ l.central ]] ? 0 : 1;
}

- (void)waitReady:(BOOL)central {
  dispatch_semaphore_wait(central ? _canWrite : _canNotify, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_MSEC));
}

- (void)stop {
  _stopped = YES;
  dispatch_source_cancel(_timer);
  if (_cm.state == CBManagerStatePoweredOn) [_cm stopScan];
  if (_pm.state == CBManagerStatePoweredOn) {
    [_pm stopAdvertising];
    [_pm removeAllServices];
  }
  for (NookLink *l in [_out allValues]) [_cm cancelPeripheralConnection:l.peripheral];
  [self dropAllOut];
  [self dropAllIn];
}

@end

static NookBLE *nook;

int nook_ble_start(int maxOut) {
  @autoreleasepool {
    if (!nook) nook = [[NookBLE alloc] initWithMaxOut:maxOut];
  }
  return 0;
}

int nook_ble_send(long long lid, const void *buf, int n) {
  @autoreleasepool {
    NookBLE *b = nook;
    if (!b || n <= 0) return -1;
    NSData *d = [NSData dataWithBytes:buf length:(NSUInteger)n];
    for (int i = 0; i < 50; i++) { // wait up to ~3 s while Bluetooth is busy
      __block int st = -1;
      __block BOOL central = NO;
      dispatch_sync(b.queue, ^{
        st = [b trySend:lid data:d central:&central];
      });
      if (st != 1) return st;
      [b waitReady:central];
    }
    return -2;
  }
}

void nook_ble_stop(void) {
  @autoreleasepool {
    NookBLE *b = nook;
    if (!b) return;
    dispatch_sync(b.queue, ^{
      [b stop];
    });
    nook = nil;
  }
}

// ---- running as our own app, as far as macOS privacy is concerned ----
//
// macOS asks the "responsible" app for Bluetooth permission: normally the
// terminal (or editor) nook was started from. If that app doesn't declare
// Bluetooth use, macOS kills nook outright. So nook starts a copy of itself
// that is responsible for itself; macOS then asks about nook directly,
// using the description embedded in the binary (ble_darwin_info.plist).

#include <dlfcn.h>
#include <errno.h>
#include <signal.h>
#include <spawn.h>
#include <sys/wait.h>
#include <crt_externs.h>

int nook_respawn_disclaimed(const char *path, char **argv) {
  int (*disclaim)(posix_spawnattr_t *, int) = dlsym(RTLD_DEFAULT, "responsibility_spawnattrs_setdisclaim");
  if (!disclaim) return -1;
  posix_spawnattr_t attr;
  if (posix_spawnattr_init(&attr) != 0) return -1;
  sigset_t def;
  sigemptyset(&def);
  int sigs[] = {SIGINT, SIGQUIT, SIGTSTP, SIGTTIN, SIGTTOU, SIGPIPE, SIGHUP, SIGTERM};
  for (unsigned i = 0; i < sizeof sigs / sizeof sigs[0]; i++) sigaddset(&def, sigs[i]);
  posix_spawnattr_setsigdefault(&attr, &def); // the copy gets normal Ctrl+C
  posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETSIGDEF);
  if (disclaim(&attr, 1) != 0) {
    posix_spawnattr_destroy(&attr);
    return -1;
  }
  pid_t pid;
  int rc = posix_spawn(&pid, path, NULL, &attr, argv, *_NSGetEnviron());
  posix_spawnattr_destroy(&attr);
  if (rc != 0) return -1;
  int status = 0;
  while (waitpid(pid, &status, 0) < 0) {
    if (errno != EINTR) return -1;
  }
  if (WIFEXITED(status)) return WEXITSTATUS(status);
  if (WIFSIGNALED(status)) return 128 + WTERMSIG(status);
  return 1;
}

// nook_ble_auth reports the Bluetooth permission: 0 not decided yet,
// 1 restricted, 2 denied, 3 allowed.
int nook_ble_auth(void) {
  if (@available(macOS 10.15, *)) return (int)CBManager.authorization;
  return 3;
}
