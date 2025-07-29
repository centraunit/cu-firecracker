FROM php:8.2-cli

WORKDIR /plugin

# Install Composer
COPY --from=composer:latest /usr/bin/composer /usr/bin/composer

# Copy composer files
COPY composer.json composer.lock* ./

# Install dependencies
RUN composer install --no-dev --no-interaction

# Copy source code
COPY . .

EXPOSE 8080

# Use PHP's built-in server for development
CMD ["php", "-S", "0.0.0.0:8080", "-t", ".", "main.php"] 