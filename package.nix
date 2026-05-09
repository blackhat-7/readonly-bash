{
  buildGoModule,
  lib,
  defaultConfigPath ? "",
}:

buildGoModule {
  pname = "readonly-bash";
  version = "0.1.0";
  src = ./.;
  vendorHash = null;
  doCheck = true;
  subPackages = [ "cmd/readonly-bash" ];
  ldflags = lib.optionals (defaultConfigPath != "") [
    "-X"
    "main.defaultConfigPath=${defaultConfigPath}"
  ];
  postInstall = ''
    ln -s $out/bin/readonly-bash $out/bin/readonly-bash-runner
  '';
}
