/* SPDX-License-Identifier: GPL-2.0-or-later
 * Quantway Windows client: Wintun L3 packets presented to tinc router mode.
 * Wintun DLL ABI comes from the official pinned signed distribution. */
#include "../system.h"
#include <windows.h>
#include <iphlpapi.h>
#include <netioapi.h>
#include "../conf.h"
#include "../device.h"
#include "../event.h"
#include "../logger.h"
#include "../net.h"
#include "../route.h"
#include "../utils.h"
#include "../xalloc.h"
#include "wintun.h"

int device_fd = -1;
char *device = NULL;
char *iface = NULL;
static HMODULE library;
static WINTUN_ADAPTER_HANDLE adapter;
static WINTUN_SESSION_HANDLE session;
static io_t receive_io;
static WINTUN_CREATE_ADAPTER_FUNC *create_adapter;
static WINTUN_OPEN_ADAPTER_FUNC *open_adapter;
static WINTUN_CLOSE_ADAPTER_FUNC *close_adapter;
static WINTUN_GET_ADAPTER_LUID_FUNC *get_luid;
static WINTUN_START_SESSION_FUNC *start_session;
static WINTUN_END_SESSION_FUNC *end_session;
static WINTUN_GET_READ_WAIT_EVENT_FUNC *get_read_event;
static WINTUN_RECEIVE_PACKET_FUNC *receive_packet;
static WINTUN_RELEASE_RECEIVE_PACKET_FUNC *release_packet;
static WINTUN_ALLOCATE_SEND_PACKET_FUNC *allocate_packet;
static WINTUN_SEND_PACKET_FUNC *wintun_send_packet;
static const GUID adapter_guid = {0x6f1091a1,0x4d91,0x44b0,{0xa2,0x0c,0x72,0x03,0xa8,0x66,0x45,0x10}};

static void close_device(void) {
    io_del(&receive_io);
    if(session) { end_session(session); session = NULL; }
    if(adapter) { close_adapter(adapter); adapter = NULL; }
    if(library) { FreeLibrary(library); library = NULL; }
    free(device); device = NULL; free(iface); iface = NULL;
}
static bool load_library(void) {
    wchar_t path[32768];
    DWORD length = GetModuleFileNameW(NULL,path,32768);
    if(!length || length >= 32768) return false;
    wchar_t *slash = wcsrchr(path,L'\\');
    if(!slash || (size_t)(slash - path) + 12 >= 32768) return false;
    wcscpy(slash+1,L"wintun.dll");
    library = LoadLibraryExW(path,NULL,LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32);
    if(!library) return false;
#define LOAD(variable, name) do { variable = (void *)GetProcAddress(library,name); if(!variable) return false; } while(0)
    LOAD(create_adapter,"WintunCreateAdapter"); LOAD(open_adapter,"WintunOpenAdapter");
    LOAD(close_adapter,"WintunCloseAdapter"); LOAD(get_luid,"WintunGetAdapterLUID");
    LOAD(start_session,"WintunStartSession"); LOAD(end_session,"WintunEndSession");
    LOAD(get_read_event,"WintunGetReadWaitEvent"); LOAD(receive_packet,"WintunReceivePacket");
    LOAD(release_packet,"WintunReleaseReceivePacket"); LOAD(allocate_packet,"WintunAllocateSendPacket");
    LOAD(wintun_send_packet,"WintunSendPacket");
#undef LOAD
    return true;
}
static bool setup_device(void) {
    char *type = NULL;
    get_config_string(lookup_config(config_tree,"DeviceType"),&type);
    get_config_string(lookup_config(config_tree,"Interface"),&iface);
    bool valid = type && !strcasecmp(type,"wintun") && (!iface || !strcmp(iface,"qtm0")) && routing_mode == RMODE_ROUTER;
    free(type);
    if(!valid) { logger(DEBUG_ALWAYS,LOG_ERR,"Client requires DeviceType=wintun, Interface=qtm0, Mode=router"); return false; }
    if(!iface) iface = xstrdup("qtm0");
    device = xstrdup("Quantway Wintun mesh");
    if(!load_library()) goto failure;
    adapter = open_adapter(L"qtm0");
    if(!adapter) adapter = create_adapter(L"qtm0",L"QuantwayMesh",&adapter_guid);
    if(!adapter) goto failure;
    NET_LUID luid; GUID actual;
    get_luid(adapter,&luid);
    if(ConvertInterfaceLuidToGuid(&luid,&actual) != NO_ERROR || memcmp(&actual,&adapter_guid,sizeof(actual))) {
        logger(DEBUG_ALWAYS,LOG_ERR,"Existing qtm0 is not the Quantway mesh adapter"); goto failure;
    }
    session = start_session(adapter,0x400000);
    if(!session || !get_read_event(session)) goto failure;
    logger(DEBUG_ALWAYS,LOG_INFO,"Quantway Wintun qtm0 adapter ready");
    return true;
failure:
    logger(DEBUG_ALWAYS,LOG_ERR,"Unable to create Quantway Wintun adapter: Windows error %lu",GetLastError());
    close_device(); return false;
}
static void handle_receive(void *data,int flags) {
    (void)data; (void)flags;
    for(unsigned budget = 0; budget < 256; budget++) {
        DWORD length = 0;
        BYTE *data = receive_packet(session,&length);
        if(!data) {
            DWORD error = GetLastError();
            if(error != ERROR_NO_MORE_ITEMS) {
                logger(DEBUG_ALWAYS,LOG_ERR,"Wintun read failed: Windows error %lu",error); event_exit();
            }
            return;
        }
        if(length >= 20 && length <= MTU-14 && (data[0] >> 4) == 4) {
            vpn_packet_t packet = {0};
            packet.len = (int)length+14; packet.priority=0;
            DATA(&packet)[12]=0x08; DATA(&packet)[13]=0x00;
            memcpy(DATA(&packet)+14,data,length);
            release_packet(session,data);
            route(myself,&packet);
        } else release_packet(session,data);
    }
}
static void enable_device(void) {
    io_add_event(&receive_io,handle_receive,NULL,get_read_event(session));
    handle_receive(NULL,0);
}
static void disable_device(void) { io_del(&receive_io); }
static bool read_packet(vpn_packet_t *packet) { (void)packet; return false; }
static bool write_packet(vpn_packet_t *packet) {
    if(!session || packet->len < 34 || DATA(packet)[12] != 0x08 || DATA(packet)[13] != 0x00 || (DATA(packet)[14] >> 4) != 4) return false;
    DWORD length = (DWORD)packet->len-14;
    BYTE *target = allocate_packet(session,length);
    if(!target) {
        if(GetLastError() == ERROR_BUFFER_OVERFLOW) return true; /* bounded ring, normal congestion drop */
        logger(DEBUG_ALWAYS,LOG_ERR,"Wintun write failed: Windows error %lu",GetLastError()); return false;
    }
    memcpy(target,DATA(packet)+14,length); wintun_send_packet(session,target); return true;
}
const devops_t os_devops = {.setup=setup_device,.close=close_device,.read=read_packet,.write=write_packet,.enable=enable_device,.disable=disable_device};
