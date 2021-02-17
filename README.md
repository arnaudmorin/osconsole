
# OpenStack Serial Console

## Introduction
This small tool will help you connect on an instance using **Serial Console** instead of SSH.
This can be useful when your instance is not accessible from network.

This is pretty much the same as using the Web Console, but using a cli tool is always easier for sysadmins.

![example](record.gif)

## Configuration on OpenStack side
Before using this tool, you must configure your OpenStack to display Serial Console.
This can be done by following this link:
https://docs.openstack.org/nova/latest/admin/remote-console-access.html#serial

## Install
### Download pre-compiled binary
See here:

### Build from sources
You can build from sources:
```
git clone https://github.com/arnaudmorin/osconsole.git
bash build.sh
```

## Usage
Before using **osconsole**, you must request a **Serial over WebSocket** URL for your instance against your OpenStack:

```
nova get-serial-console
```

(note that, as of 2021, this is still not possible to do it using openstack client, you still need to use nova for this)

The, just copy/paste this URL to osconsole:

```
osconsole 'ws://51.89.5.214:6083/?token=2e1ba41d-61f2-4b3e-a26d-777fdb56665d'
+-----------------------------------------+ 
|Connected. Type "Ctrl+[ d" to disconnect.| 
+-----------------------------------------+ 

login as 'cirros' user. default password: 'gocubsgo'. use 'sudo' for root.
n1 login:
```

You are now connected!

## Authors
This project has been inspired from:
https://github.com/hironobu-s/novassh

Original author: Hironobu Saitoh - [hiro@hironobu.org](mailto:hiro@hironobu.org)

osconsole author: Arnaud Morin - [arnaud.morin@gmail.com](mailto:arnaud.morin@gmail.com)

