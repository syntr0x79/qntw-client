/* SPDX-License-Identifier: GPL-2.0-or-later
 * All public transport sockets are pinned to the selected physical interface.
 * This does not replace the parent's WFP kill switch. */
#define QNTW_SOCKET_GUARD_IMPLEMENTATION
#include "socket_guard.h"
#include <errno.h>
#include <stdint.h>
#include <limits.h>
#include <stdlib.h>
#include <string.h>

static int denied(int error) { errno = EPERM; WSASetLastError(error); return -1; }
static int physical_index(DWORD *index) {
    const char *text = getenv("QUANTWAY_PHYSICAL_IFINDEX");
    if(!text || !*text) return denied(WSAEACCES);
    uint64_t value = 0;
    for(const char *p = text; *p; p++) {
        if(*p < '0' || *p > '9') return denied(WSAEINVAL);
        value = value * 10 + (unsigned)(*p - '0');
        if(value > 0x00ffffffU) return denied(WSAEINVAL);
    }
    if(!value) return denied(WSAEINVAL);
    *index = htonl((DWORD)value);
    return 0;
}
int qntw_guarded_socket(int family, int type, int protocol) {
    if(family != AF_INET || (type != SOCK_STREAM && type != SOCK_DGRAM)) return denied(WSAEAFNOSUPPORT);
    DWORD index;
    if(physical_index(&index)) return -1;
    SOCKET fd = socket(family, type, protocol);
    if(fd == INVALID_SOCKET) return -1;
    /* tinc 1.1pre18 uses signed int descriptors throughout its Windows IO API. */
    if(fd > INT_MAX) { closesocket(fd); return denied(WSAEMFILE); }
    if(setsockopt(fd, IPPROTO_IP, IP_UNICAST_IF, (const char *)&index, sizeof(index))) {
        int error = WSAGetLastError(); closesocket(fd); return denied(error);
    }
    return (int)fd;
}
static int destination_interface(SOCKET fd, const struct sockaddr *address, int length) {
    if(!address || address->sa_family != AF_INET || length < (int)sizeof(struct sockaddr_in)) return denied(WSAEAFNOSUPPORT);
    const struct sockaddr_in *ip = (const struct sockaddr_in *)address;
    DWORD index;
    if((ntohl(ip->sin_addr.s_addr) & 0xff000000UL) == 0x7f000000UL) {
        /* Authenticated native control remains local even on a wildcard VPN listener. */
        index = 0;
    } else if(physical_index(&index)) return -1;
    return setsockopt(fd, IPPROTO_IP, IP_UNICAST_IF, (const char *)&index, sizeof(index));
}
int qntw_guarded_connect(SOCKET fd, const struct sockaddr *address, int length) {
    if(destination_interface(fd,address,length)) return -1;
    return connect(fd,address,length);
}
int qntw_guarded_bind(SOCKET fd, const struct sockaddr *address, int length) {
    if(destination_interface(fd,address,length)) return -1;
    return bind(fd,address,length);
}
int qntw_guarded_accept(SOCKET listener, struct sockaddr *address, int *length) {
    SOCKET fd = accept(listener,address,length);
    if(fd == INVALID_SOCKET) return -1;
    if(fd > INT_MAX) { closesocket(fd); return denied(WSAEMFILE); }
    struct sockaddr_in peer;
    int peer_length = sizeof(peer);
    if(getpeername(fd,(struct sockaddr *)&peer,&peer_length) || destination_interface(fd,(struct sockaddr *)&peer,peer_length)) {
        int error = WSAGetLastError(); closesocket(fd); return denied(error);
    }
    return (int)fd;
}
int qntw_numeric_getaddrinfo(const char *node, const char *service, const struct addrinfo *hints, struct addrinfo **result) {
    struct addrinfo numeric = {0};
    if(hints) numeric = *hints;
    numeric.ai_flags |= AI_NUMERICHOST | AI_NUMERICSERV;
    return getaddrinfo(node, service, &numeric, result);
}
int qntw_numeric_getnameinfo(const struct sockaddr *address, socklen_t length, char *host, socklen_t host_length, char *service, socklen_t service_length, int flags) {
    return getnameinfo(address, length, host, host_length, service, service_length, (flags & ~NI_NAMEREQD) | NI_NUMERICHOST | NI_NUMERICSERV);
}
