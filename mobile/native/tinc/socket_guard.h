/* SPDX-License-Identifier: GPL-2.0-or-later */
#ifndef QNTW_TINC_SOCKET_GUARD_H
#define QNTW_TINC_SOCKET_GUARD_H

#include <sys/socket.h>
#include <netdb.h>

int qntw_guarded_socket(int family, int type, int protocol);
int qntw_numeric_getaddrinfo(const char *node, const char *service,
                            const struct addrinfo *hints, struct addrinfo **result);
int qntw_numeric_getnameinfo(const struct sockaddr *address, socklen_t length,
                           char *host, socklen_t host_length,
                           char *service, socklen_t service_length, int flags);

/* Included after system declarations in tinc's system.h. All compiled daemon
 * sources, including ICMP helpers and optional device backends, use this guard.
 * The implementation itself intentionally calls libc without these macros. */
#ifndef QNTW_SOCKET_GUARD_IMPLEMENTATION
#define socket qntw_guarded_socket
#define getaddrinfo qntw_numeric_getaddrinfo
#define getnameinfo qntw_numeric_getnameinfo
#endif
#endif
