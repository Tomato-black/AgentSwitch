#!/bin/sh
# 修正 Linux 用户级安装的桌面入口——在 install.sh 之后运行。
#
# MyGo 的 install.sh 生成 ~/.local/share/applications/agentswitch.desktop，
# 并把 Icon= 改写成 $app_dir/agentswitch.png（绝对路径）。Ubuntu 的 GNOME
# Shell 42 会按**应用 id** 缓存图标加载结果：该 id 一旦有过一次加载失败
# （例如安装写入与 Shell 扫描的时序冲突），之后即使图标文件有效也会一直
# 显示 application-x-executable（灰色齿轮），直到注销会话。同 id 重装
# 不会刷新这个缓存。
#
# 这里改用反向域名的稳定 id（com.agentswitch.app.desktop）+ hicolor 主题
# 图标（Icon=agentswitch），并删除 MyGo 生成的旧入口，避免重装后又回到
# 缓存失败的 id。图标取材于安装目录的 agentswitch.png（构建产物已输出
# 256px，见 internal/desktop/gen_icons.go）。
set -eu

app_dir="${HOME}/.local/agentswitch.app"
apps="${HOME}/.local/share/applications"
icons="${HOME}/.local/share/icons/hicolor"

if [ ! -x "${app_dir}/agentswitch" ]; then
    echo "linux-desktop.sh: ${app_dir}/agentswitch not found; run install.sh first" >&2
    exit 1
fi

mkdir -p "${icons}/256x256/apps"
cp "${app_dir}/agentswitch.png" "${icons}/256x256/apps/agentswitch.png"
# 用户级 hicolor 缺 index.theme 时主题目录不被当作有效图标主题。
if [ ! -f "${icons}/index.theme" ] && [ -f /usr/share/icons/hicolor/index.theme ]; then
    cp /usr/share/icons/hicolor/index.theme "${icons}/index.theme"
fi

mkdir -p "${apps}"
cat > "${apps}/com.agentswitch.app.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=AgentSwitch
Exec="${app_dir}/agentswitch"
Icon=agentswitch
Categories=Utility;
Terminal=false
EOF

# MyGo 每次安装都会重新生成该入口；留着会多出一个灰色齿轮条目。
rm -f "${apps}/agentswitch.desktop"

if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -f -t "${icons}" >/dev/null 2>&1 || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "${apps}" >/dev/null 2>&1 || true
fi

echo "Installed desktop entry: ${apps}/com.agentswitch.app.desktop"
