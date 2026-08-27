fn main() {
    if cfg!(feature = "vpx") {
        match pkg_config::Config::new()
            .atleast_version("1.0")
            .probe("vpx")
        {
            Ok(_) => {}
            Err(e) => {
                println!(
                    "cargo:warning=pkg-config probe vpx failed: {}, using fallback",
                    e
                );
                println!("cargo:rustc-link-search=/usr/local/lib");
                println!("cargo:rustc-link-lib=dylib=vpx");
            }
        }
    }

    if cfg!(feature = "x265") {
        match pkg_config::Config::new()
            .atleast_version("3.0")
            .probe("x265")
        {
            Ok(_) => {}
            Err(e) => {
                println!(
                    "cargo:warning=pkg-config probe x265 failed: {}, using fallback",
                    e
                );
                println!("cargo:rustc-link-search=/usr/local/lib");
                println!("cargo:rustc-link-lib=dylib=x265");
            }
        }
    }

    if cfg!(feature = "dav1d") {
        match pkg_config::Config::new()
            .atleast_version("1.0")
            .probe("dav1d")
        {
            Ok(_) => {}
            Err(e) => {
                println!(
                    "cargo:warning=pkg-config probe dav1d failed: {}, using fallback",
                    e
                );
                println!("cargo:rustc-link-search=/usr/local/lib");
                println!("cargo:rustc-link-lib=dylib=dav1d");
            }
        }
    }

    if cfg!(feature = "libaom") {
        match pkg_config::Config::new()
            .atleast_version("3.0")
            .probe("aom")
        {
            Ok(_) => {}
            Err(e) => {
                println!(
                    "cargo:warning=pkg-config probe aom failed: {}, using fallback",
                    e
                );
                println!("cargo:rustc-link-search=/usr/local/lib");
                println!("cargo:rustc-link-lib=dylib=aom");
            }
        }
    }

    if cfg!(feature = "libde265") {
        match pkg_config::Config::new()
            .atleast_version("1.0")
            .probe("libde265")
        {
            Ok(_) => {}
            Err(e) => {
                println!(
                    "cargo:warning=pkg-config probe libde265 failed: {}, using fallback",
                    e
                );
                println!("cargo:rustc-link-search=/usr/local/lib");
                println!("cargo:rustc-link-lib=dylib=de265");
            }
        }
    }

    if cfg!(feature = "videotoolbox") && cfg!(target_os = "macos") {
        println!("cargo:rustc-link-lib=framework=VideoToolbox");
        println!("cargo:rustc-link-lib=framework=CoreMedia");
        println!("cargo:rustc-link-lib=framework=CoreVideo");
        println!("cargo:rustc-link-lib=framework=CoreFoundation");
    }
}
