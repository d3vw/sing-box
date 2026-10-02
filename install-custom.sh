#!/usr/bin/env bash
#
# 用本仓库构建的 sing-box 替换 sing-box-extended（另一个项目），
# 并安装与 go.mod 匹配的新版 libcronet.so（修复 naive 出站的符号错误）。
#
# 用法：  sudo bash install-custom.sh
#
set -euo pipefail

REPO=/home/grey/Amateur/sing-box
BIN_SRC="$REPO/sing-box"
SO_SRC="$REPO/lib/libcronet.so"

# 必须以 root 运行
if [[ $EUID -ne 0 ]]; then
    echo "请用 sudo 运行： sudo bash $0" >&2
    exit 1
fi

# 构建产物必须存在
[[ -x "$BIN_SRC" ]] || { echo "找不到二进制：$BIN_SRC（先在仓库里构建）" >&2; exit 1; }
[[ -f "$SO_SRC"  ]] || { echo "找不到 libcronet.so：$SO_SRC" >&2; exit 1; }

echo "==> 1. 停止 sing-box 服务"
systemctl stop sing-box 2>/dev/null || true

echo "==> 2. 删除 sing-box-extended（另一个项目）"
rm -rf /usr/local/lib/sing-box-extended

echo "==> 3. 删除旧的 /usr/bin/sing-box（指向 extended 的软链）"
rm -f /usr/bin/sing-box

echo "==> 4. 删除孤儿旧版 libcronet.so（无包归属、缺新符号、是报错根源）"
rm -f /usr/lib/libcronet.so /usr/bin/libcronet.so

echo "==> 5. 安装本仓库构建的二进制 -> /usr/bin/sing-box"
install -m755 "$BIN_SRC" /usr/bin/sing-box

echo "==> 6. 安装匹配的新版 libcronet.so -> /usr/local/lib（二进制 RUNPATH 优先在此查找）"
install -d /usr/local/lib
install -m644 "$SO_SRC" /usr/local/lib/libcronet.so
ldconfig

echo "==> 7. 验证（LD_BIND_NOW=1 强制解析全部符号，缺符号会立即报错）"
LD_BIND_NOW=1 /usr/bin/sing-box version

echo "==> 8. 校验配置"
/usr/bin/sing-box -D /var/lib/sing-box -C /etc/sing-box check && echo "配置 OK"

echo "==> 8.5. 确保 /etc/sing-box 属主与服务运行用户一致（quota/dashboard-ui 等需要写回配置）"
if [[ -d /etc/sing-box ]]; then
    SERVICE_USER="$(systemctl show sing-box -p User --value 2>/dev/null)"
    if [[ -n "$SERVICE_USER" && "$SERVICE_USER" != "root" ]]; then
        chown -R "$SERVICE_USER":"$SERVICE_USER" /etc/sing-box
    fi
fi

echo "==> 9. 重启服务"
systemctl restart sing-box
sleep 1
systemctl --no-pager --full status sing-box | head -15

echo
echo "完成。"
echo "提示：/usr/bin/sing-box 这个路径名义上仍属于 pacman 包 sing-box，"
echo "      日后 'pacman -Syu' 升级该包可能覆盖此二进制；如需彻底脱离 pacman，"
echo "      可执行： sudo pacman -Rdd sing-box   （仅移除包记录，不动你刚装的文件）"
