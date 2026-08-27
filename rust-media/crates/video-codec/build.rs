fn main() {
    // 链接系统 libvpx（通过 pkg-config）
    match pkg_config::Config::new()
        .atleast_version("1.0")
        .probe("vpx")
    {
        Ok(_) => {
            // pkg-config 已经输出 rustc-link-lib 和 rustc-link-search
        }
        Err(e) => {
            // fallback：手动指定
            println!(
                "cargo:warning=pkg-config probe failed: {}, using fallback",
                e
            );
            println!("cargo:rustc-link-search=/usr/local/lib");
            println!("cargo:rustc-link-lib=dylib=vpx");
        }
    }
}
