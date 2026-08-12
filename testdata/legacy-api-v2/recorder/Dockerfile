# The corpus recorder's environment, pinned so the recording is
# reproducible: PHP 8.2 (eveseat/api requires ^8.1), pdo_mysql to talk to
# the MySQL 8 the legacy migrations are applied to, and gd because
# Seat\Web\Models\Squads\Squad::getLogoAttribute() renders a placeholder
# avatar through intervention/image whenever a squad has no stored logo —
# which is part of what legacy puts on the wire.
FROM php:8.2-cli

RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends \
      unzip git libpng-dev libjpeg62-turbo-dev libfreetype6-dev libzip-dev \
 && docker-php-ext-configure gd --with-freetype --with-jpeg \
 && docker-php-ext-install -j"$(nproc)" pdo_mysql gd zip \
 && rm -rf /var/lib/apt/lists/*

COPY --from=composer:2 /usr/bin/composer /usr/local/bin/composer

WORKDIR /app
