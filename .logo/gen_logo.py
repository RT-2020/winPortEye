# -*- coding: utf-8 -*-
"""PortEye logo 生成器（设计迭代用，一次性脚本）

概念：端口之眼 —— 眼睛的虹膜由环形端口（小圆点）构成，瞳孔是网络节点亮点。
输出：256 主图标 + 16 托盘简化图标 + 多尺寸 ICO。
"""
import math
import os
from PIL import Image, ImageDraw

OUT = os.path.join(os.path.dirname(__file__), "..", "assets")
os.makedirs(OUT, exist_ok=True)

S = 256


def lerp(a, b, t):
    return a + (b - a) * t


def draw_main(size):
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    r = size
    # 圆角方形深蓝渐变底
    margin = int(r * 0.04)
    rect = [margin, margin, r - margin, r - margin]
    radius = int(r * 0.22)
    for y in range(margin, r - margin):
        t = (y - margin) / (r - 2 * margin)
        col = (int(lerp(10, 28, t)), int(lerp(24, 62, t)), int(lerp(58, 112, t)), 255)
        d.line([(margin, y), (r - margin, y)], fill=col)
    # 圆角遮罩（对渐变层裁剪）
    mask = Image.new("L", (r, r), 0)
    ImageDraw.Draw(mask).rounded_rectangle(rect, radius=radius, fill=255)
    base = Image.new("RGBA", (r, r), (0, 0, 0, 0))
    base.paste(img, (0, 0), mask)
    img, d = base, ImageDraw.Draw(base)

    cx, cy = r / 2, r / 2
    # 虹膜环：端口小圆点（8 个，青色高亮，外侧 2 个暗色表示"注视"）
    iris_r = r * 0.30
    dot_r = r * 0.052
    ports = 8
    for i in range(ports):
        ang = math.pi * 2 * i / ports - math.pi / 2
        px = cx + math.cos(ang) * iris_r
        py = cy + math.sin(ang) * iris_r
        if i in (2, 6):
            col = (28, 70, 120, 255)  # 暗色端口（没被占用的口）
        else:
            col = (34, 211, 238, 255)  # 青色亮口
        d.ellipse([px - dot_r, py - dot_r, px + dot_r, py + dot_r], fill=col)

    # 瞳孔：深蓝圆 + 中心亮点（网络节点）
    pup_r = r * 0.165
    d.ellipse([cx - pup_r, cy - pup_r, cx + pup_r, cy + pup_r], fill=(7, 18, 40, 255))
    hl_r = r * 0.062
    d.ellipse([cx - hl_r, cy - hl_r, cx + hl_r, cy + hl_r], fill=(140, 240, 255, 255))
    # 高光小点
    s_r = r * 0.028
    d.ellipse([cx + pup_r * 0.35 - s_r, cy - pup_r * 0.45 - s_r,
               cx + pup_r * 0.35 + s_r, cy - pup_r * 0.45 + s_r], fill=(255, 255, 255, 230))
    return img


def draw_tray(size):
    """托盘 16x16 简化版：实底 + 瞳孔亮点（小尺寸下环点不可辨，只留眼睛核心）"""
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    cx = cy = size / 2
    # 圆角底
    m = size * 0.06
    r = int(size * 0.22)
    d.rounded_rectangle([m, m, size - m, size - m], radius=r, fill=(13, 32, 72, 255))
    # 瞳孔
    pr = size * 0.21
    d.ellipse([cx - pr, cy - pr, cx + pr, cy + pr], fill=(7, 18, 40, 255))
    # 亮点
    hr = size * 0.075
    d.ellipse([cx - hr, cy - hr, cx + hr, cy + hr], fill=(140, 240, 255, 255))
    return img


def build_ico():
    """手写 ICO 容器：PIL 对 ICO 的 append_images/sizes 处理不可靠（实测只落 1 个尺寸），
    这里按 ICO 规范自拼：6B 头 + 16B/条目 + 各尺寸 PNG 数据（Vista+ 支持 PNG 条目）。"""
    import io
    import struct

    sizes = [16, 24, 32, 48, 64, 128, 256]
    entries = []
    datas = []
    offset = 6 + 16 * len(sizes)
    for s in sizes:
        img = draw_main(s) if s >= 32 else draw_tray(s)
        buf = io.BytesIO()
        img.save(buf, format="PNG")
        png = buf.getvalue()
        w = 0 if s == 256 else s
        h = 0 if s == 256 else s
        entries.append(struct.pack("<BBBBHHII", w, h, 0, 0, 1, 32, len(png), offset))
        datas.append(png)
        offset += len(png)
    header = struct.pack("<HHH", 0, 1, len(sizes))
    with open(os.path.join(OUT, "porteye.ico"), "wb") as f:
        f.write(header)
        for e in entries:
            f.write(e)
        for d in datas:
            f.write(d)
    print("porteye.ico done (handcrafted)")


if __name__ == "__main__":
    draw_main(256).save(os.path.join(OUT, "logo-256.png"))
    draw_tray(16).resize((256, 256), Image.NEAREST).save(os.path.join(OUT, "logo-tray-16x.png"))
    build_ico()
    print("done")
