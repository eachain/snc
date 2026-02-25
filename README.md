# snc

snc = ssh + ncat。

用于文件上传下载、端口转发，当公司内部部署了[开源堡垒机](https://jumpserver.org/)时。

## 背景

当公司为安全需求部署[开源堡垒机](https://jumpserver.org/)时，所有服务器登录、文件上传下载、数据库访问，都必须经堡垒机（又称跳板机）。

经堡垒机登录服务器可借由ssh免秘+expect命令实现，不是snc要解决的问题。

文件上传下载需登录到堡垒机web端操作，共计四步：登录，进入文件管理，选择机器，文件操作。整体流程复杂，耗时长。

数据库访问也是重灾区，各数据库均需命令行访问，缺少图形界面，极大地降低了工作效率。Redis提供了临时token的访问方式，供图形界面访问，但每次均需多步骤长耗时才能获取token。MySQL提供了图形界面访问方式，但提供的工具不一定是开发人员常用的，需额外学习成本。MongoDB则完全不支持图形界面。

综上，需要一款工具来绕过堡垒机，由开发运维人员能快速实现文件上传下载，以及数据库访问。

## 架构

```
+------+  ctrl channel  +-------+
|      | <------------> | LINUX | <->+
| USER |                +-------+    |
|      |                             | data channel
| CTRL |  data channel  +-------+    |
|      | <------------> | PROXY | <->+
+------+                +-------+
```

USER发命令给LINUX执行，数据传输走PROXY。

依赖：

- linux端：nc(netcat或ncat都可)、rsync；
- user本地：rsync；
- proxy端：安装sncd（见sncd.go）；
- linux访问proxy没有端口限制，即linux可访问proxy主机所有TCP端口；
- user访问proxy正常。

## 端口映射

使用示例：

`snc f redis-host:6379 linux.host.name`

原理：

- 假设需要映射的数据库地址为`DEST`；
- 在PROXY申请两个数据通道`CHANNEL1`和`CHANNEL2`；
- 在LINUX执行`nc --recv-only CHANNEL1 | nc DEST | nc --send-only CHANNEL2`；
- 本地写数据到`CHANNEL1`，从`CHANNEL2`读数据。

## 文件传输

上传示例：

`snc f local/file/path linux.host.name:remote/file/path`

下载示例：

`snc f linux.host.name:remote/file/path local/file/path`

原理：

- 本地监听端口开启ssh服务，启动`rsync`命令连接到ssh服务，关闭ssh服务；
- 获取本地`rsync`需要在ssh远程执行的`rsync`命令（搜索：rsync工作原理）；
- 在PROXY申请两个数据通道`CHANNEL1`和`CHANNEL2`；
- 在LINUX执行`nc --recv-only CHANNEL1 | rsync --params | nc --send-only CHANNEL2`；
- 本地`rsync`通过ssh连接，将数据写到`CHANNEL1`，并从`CHANNEL2`读远程`rsync`返回的数据，完成文件上传下载。

## 数据通道加密

加密仅发生在USER<->PROXY，加密不是为了安全，而是应对公司ACL规则的BUG：只要发出的数据包以`*2\r\n$4\r\n`开头，ACL就会强制断开TCP连接。如果没有该BUG，本身应该是明文传输。

## 为什么将控制与数据通道分离？

控制通道本身有波特(Baud)速率限制，数据库端口映射慢一些还能接受，文件上传下载速率以KB/s计算，不能提升办公效率。

## sncd部署

可简单地以`nohup ./sncd &`方式启动。默认监听端口"65533"，如果需要改动，需要添加启动参数`-p YOUR_PORT`。

## snc编译

为方便使用，snc命令的部分参数可以在编译时写入合适的默认值，或用`alias`命令设置默认值。

编译填充默认值方式：

- jumper: main.go:11 tag添加`dft:"jump.server.host:port"`；
- user: main.go:12 tag添加`dft:"USER"`。
- proxy: main.go:14 tag添加`dft:"proxy.host:port"`

用`alias`设置默认值的方式：

`alias snc='snc --jumper jump.server.host:port --user USER' --proxy proxy.host:port`
