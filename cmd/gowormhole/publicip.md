# publicip

Discover your public ip address.

## Usage

```
$ publicip
113.43.xxx.xxx
```

## References

* [pion/webrtc: Pure Go implementation of the WebRTC API](https://github.com/pion/webrtc)
* [JavaScriptでグローバルIPを取得する(パブリックSTUNサーバーを利用) - Qiita](https://qiita.com/azechi/items/1a7832e346f42402cca6)

```js
// Web 浏览器实现 STUN 客户端
// STUN 服务器可用于在 NAT 之外查找 IP 地址。
// 互联网上有公开的 STUN 服务器。
// Web 浏览器实现 STUN 客户端。
// 您可以使用 WebRTC API 获取 STUN 检查的 IP 地址。
function getIPAddresses() {
    // stun.l.google.com:19302 使用谷歌的公共 STUN 服务器 
    const S = "stun.l.google.com:19302";

    return new Promise(resolve => {
        const pc = new RTCPeerConnection({
            iceServers: [
                {
                    urls: ["stun:" + S]
                }
            ]
        });

        const rslt = [];
        pc.onicecandidate = e => {
            if (!e.candidate) {
                return resolve(rslt);
            }
            const [ip, , , type] = e.candidate.candidate.split(" ", 8).slice(4);
            if (type == "srflx") { // 本地地址可以通过类型 “host” 获取, 类型 “srflx” 表示服务器自反
                rslt.push(ip);
            }
        };

        pc.onnegotiationneeded = async () => {
            await pc.setLocalDescription(await pc.createOffer());
        };
        // 如果没有 mediatrack 或 datachannel，iceGathering 将无法启动
        pc.createDataChannel("");
    });
}
```
