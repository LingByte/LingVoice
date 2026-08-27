fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proto_path = "../../proto/media_node.proto";

    // 找到 protoc 的 well-known types include 路径
    // 优先用环境变量，其次尝试常见路径
    let well_known_include = std::env::var("PROTOC_INCLUDE").unwrap_or_else(|_| {
        // 尝试常见路径
        let candidates = [
            "/tmp/protoc_install/include",
            "/usr/local/include",
            "/usr/include",
        ];
        for c in &candidates {
            if std::path::Path::new(c)
                .join("google/protobuf/timestamp.proto")
                .exists()
            {
                return c.to_string();
            }
        }
        "/usr/local/include".to_string()
    });

    tonic_build::configure()
        .build_server(true)
        .build_client(false)
        .compile_protos(&[proto_path], &["../../proto", &well_known_include])?;

    // 触发重新编译
    println!("cargo:rerun-if-changed={}", proto_path);
    println!("cargo:rerun-if-changed=../../proto");

    Ok(())
}
